package job

import (
	"bytes"
	"errors"
	"fmt"
	"goto/pkg/global"
	"goto/pkg/http/invocation"
	"goto/pkg/util"
	"log"
	"net/http"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/mux"
)

type CommandJobTask struct {
  Cmd  string   `json:"cmd"`
  Args []string `json."args"`
}

type HttpJobTask struct {
  invocation.InvocationSpec
  ParseJSON bool `json:"parseJSON"`
}

type JobResult struct {
  Index       string
  Finished    bool
  ResultTime  time.Time
  Data        interface{}
}

type JobRunInfo struct {
  index       int
  running     bool
  stopChannel chan bool
  doneChannel chan bool
  lock        sync.RWMutex
}

type Job struct {
  ID          string          `json:"id"`
  Task        interface{}     `json:"task"`
  Auto        bool            `json:"auto"`
  Delay       string          `json:"delay"`
  Count       int             `json:"count"`
  MaxResults  int             `json:"maxResults"`
  KeepFirst   bool            `json:"keepFirst"`
  delayD      time.Duration   `json:"-"`
  httpTask    *HttpJobTask    `json:"-"`
  commandTask *CommandJobTask `json:"-"`
  jobRun      *JobRunInfo     `json:"-"`
  jobResults  []*JobResult    `json:"-"`
  resultCount int             `json:"-"`
  lock        sync.RWMutex    `json:"-"`
}

type PortJobs struct {
  jobs          map[string]*Job
  jobRunCounter int
  listenerPort  string
  lock          sync.RWMutex
}

var (
  Handler      util.ServerHandler   = util.ServerHandler{Name: "jobs", SetRoutes: SetRoutes}
  portJobs     map[string]*PortJobs = map[string]*PortJobs{}
  portJobsLock sync.RWMutex
)

func SetRoutes(r *mux.Router, parent *mux.Router, root *mux.Router) {
  jobsRouter := r.PathPrefix("/jobs").Subrouter()
  util.AddRoute(jobsRouter, "/add", addJob, "POST")
  util.AddRoute(jobsRouter, "/{jobs}/remove", removeJob, "POST")
  util.AddRoute(jobsRouter, "/clear", clearJobs, "POST")
  util.AddRoute(jobsRouter, "/{jobs}/run", runJobs, "POST")
  util.AddRoute(jobsRouter, "/run/all", runJobs, "POST")
  util.AddRoute(jobsRouter, "/{jobs}/stop", stopJobs, "POST")
  util.AddRoute(jobsRouter, "/stop/all", stopJobs, "POST")
  util.AddRoute(jobsRouter, "/{job}/results", getJobResults, "GET")
  util.AddRoute(jobsRouter, "", getJobs, "GET")
}

func (pj *PortJobs) init() {
  pj.lock.Lock()
  defer pj.lock.Unlock()
  pj.jobs = map[string]*Job{}
}

func (pj *PortJobs) AddJob(job *Job) {
  pj.lock.Lock()
  defer pj.lock.Unlock()
  job.delayD, _ = time.ParseDuration(job.Delay)
  if job.Count == 0 {
    job.Count = 1
  }
  if job.MaxResults <= 0 {
    job.MaxResults = 3
  }
  pj.jobs[job.ID] = job
  if job.Auto {
    go func() {
      log.Printf("Auto-invoking Job: %s\n", job.ID)
      pj.runJobs([]*Job{job})
    }()
  }
}

func (pj *PortJobs) removeJobs(jobs []string) {
  pj.lock.Lock()
  defer pj.lock.Unlock()
  for _, j := range jobs {
    job := pj.jobs[j]
    if job != nil {
      job.lock.Lock()
      defer job.lock.Unlock()
      delete(pj.jobs, j)
    }
  }
}

func (pj *PortJobs) getJobResults(name string) []interface{} {
  pj.lock.RLock()
  defer pj.lock.RUnlock()
  results := []interface{}{}
  job := pj.jobs[name]
  if job != nil {
    job.lock.RLock()
    for _, r := range job.jobResults {
      results = append(results, r)
    }
    job.lock.RUnlock()
  }
  return results
}

func (pj *PortJobs) getJobsToRun(names []string) []*Job {
  pj.lock.RLock()
  defer pj.lock.RUnlock()
  var jobsToRun []*Job
  if len(names) > 0 {
    for _, j := range names {
      if job, found := pj.jobs[j]; found {
        jobsToRun = append(jobsToRun, job)
      }
    }
  } else {
    if len(pj.jobs) > 0 {
      for _, job := range pj.jobs {
        jobsToRun = append(jobsToRun, job)
      }
    }
  }
  return jobsToRun
}

func storeJobResultsInRegistryLocker(job *Job) {
  if global.UseLocker && global.RegistryURL != "" {
    key := "job_"+job.ID + "_" + strconv.Itoa(job.jobRun.index)
    url := global.RegistryURL + "/registry/peers/" + global.PeerName + "/locker/store/" + key
    if resp, err := http.Post(url, "application/json",
      strings.NewReader(util.ToJSON(job.jobResults))); err == nil {
      defer resp.Body.Close()
      log.Printf("Stored job results under locker key %s for peer [%s] with registry [%s]\n", key, global.PeerName, global.RegistryURL)
    }
  }
}

func storeCommandResult(data string, job *Job, last bool) {
  job.lock.Lock()
  defer job.lock.Unlock()
  job.resultCount++
  index := strconv.Itoa(job.jobRun.index) + "." + strconv.Itoa(job.resultCount)
  jobResult := &JobResult{Index: index, Finished: last, ResultTime: time.Now(), Data: data}
  job.jobResults = append(job.jobResults, jobResult)
  if len(job.jobResults) > job.MaxResults {
    if job.KeepFirst {
      job.jobResults = append(job.jobResults[:1], job.jobResults[2:]...)
    } else {
      job.jobResults = job.jobResults[1:]
    }
  }
  storeJobResultsInRegistryLocker(job)
}

func runCommandAndStoreResult(job *Job, last bool) {
  runLlock := sync.Mutex{}
  job.lock.RLock()
  cmd := exec.Command(job.commandTask.Cmd, job.commandTask.Args...)
  job.lock.RUnlock()
  var stdout bytes.Buffer
  var stderr bytes.Buffer
  cmd.Stdout = &stdout
  cmd.Stderr = &stderr
  if err := cmd.Run(); err != nil {
    msg := fmt.Sprintf("Failed to execute command [%s] with error [%s] [%s]", job.commandTask.Cmd, stderr.String(), err.Error())
    log.Println(msg)
    storeCommandResult(msg, job, last)
    return
  }
  c := make(chan string)
  defer close(c)
  stop := false
  go func() {
    cmd.Wait()
    runLlock.Lock()
    if !stop {
      out := stdout.String()
      err := stderr.String()
      if len(out) > 0 {
        c <- out
      } else {
        c <- err
      }
    }
    runLlock.Unlock()
  }()
  var stopChannel chan bool
  job.lock.RLock()
  stopChannel = job.jobRun.stopChannel
  job.lock.RUnlock()
  select {
  case <-stopChannel:
    runLlock.Lock()
    stop = true
    job.lock.Lock()
    job.jobRun.running = false
    job.lock.Unlock()
    if err := cmd.Process.Kill(); err != nil {
      log.Printf("Failed to stop command [%s] with error [%s]\n", job.commandTask.Cmd, err.Error())
    }
    runLlock.Unlock()
  case out := <-c:
    storeCommandResult(out, job, last)
  }
}

func initJobRun(job *Job, index int) bool {
  if job == nil || job.jobRun != nil || (job.commandTask == nil && job.httpTask == nil) {
    return false
  }
  job.lock.Lock()
  defer job.lock.Unlock()
  job.jobResults = []*JobResult{}
  job.resultCount = 0
  job.jobRun = &JobRunInfo{}
  job.jobRun.index = index
  job.jobRun.stopChannel = make(chan bool)
  job.jobRun.doneChannel = make(chan bool, 10)
  return true
}

func runCommandJob(job *Job) {
  log.Printf("Starting Command job: %s\n", job.ID)
  jobRun := job.jobRun
  jobRun.lock.Lock()
  jobRun.running = true
  jobRun.lock.Unlock()
  for i := 0; i < job.Count; i++ {
    runCommandAndStoreResult(job, i == job.Count-1)
    if !jobRun.running {
      break
    }
    time.Sleep(job.delayD)
  }
  job.lock.Lock()
  jobRun.lock.Lock()
  job.jobResults[len(job.jobResults)-1].Finished = true
  jobRun.doneChannel <- true
  close(jobRun.stopChannel)
  close(jobRun.doneChannel)
  jobRun.lock.Unlock()
  job.jobRun = nil
  job.lock.Unlock()
  log.Printf("job [%s] Finished \n", job.ID)
}

func runCommandJobs(jobs []*Job) {
  for _, job := range jobs {
    go runCommandJob(job)
  }
}

func storeHttpResult(result *invocation.InvocationResult, jobs []*Job, last bool) {
  for _, job := range jobs {
    job.lock.Lock()
    if job.httpTask != nil && job.httpTask.Name == result.TargetName {
      job.resultCount++
      index := strconv.Itoa(job.jobRun.index) + "." + strconv.Itoa(job.resultCount)
      jobResult := &JobResult{Index: index, Finished: last, ResultTime: time.Now()}
      if job.httpTask.ParseJSON {
        json := map[string]interface{}{}
        if err := util.ReadJson(result.Body, &json); err != nil {
          log.Printf("Failed reading response JSON: %s\n", err.Error())
          jobResult.Data = result.Body
        } else {
          jobResult.Data = json
        }
      } else {
        jobResult.Data = result.Body
      }
      job.jobResults = append(job.jobResults, jobResult)
      if len(job.jobResults) > job.MaxResults {
        if job.KeepFirst {
          job.jobResults = append(job.jobResults[:1], job.jobResults[2:]...)
        } else {
          job.jobResults = job.jobResults[1:]
        }
      }
    }
    storeJobResultsInRegistryLocker(job)
    job.lock.Unlock()
  }
}

func invokeHttpTargets(jobs []*Job, last bool) {
  targets := []*invocation.InvocationSpec{}
  for _, job := range jobs {
    job.lock.RLock()
    targets = append(targets, &job.httpTask.InvocationSpec)
    job.lock.RUnlock()
  }
  if len(targets) > 0 {
    ic := invocation.RegisterInvocation(jobs[len(jobs)-1].jobRun.index)
    c := make(chan bool)
    go func() {
      invocation.InvokeTargets(targets, ic, true)
      c <- true
    }()
    done := false
  Results:
    for {
      select {
      case done = <-ic.DoneChannel:
        break Results
      case <-ic.StopChannel:
        break Results
      case result := <-ic.ResultChannel:
        if result != nil {
          storeHttpResult(result, jobs, last)
        }
      }
    }
    <-c
    if done {
    MoreResults:
      for {
        select {
        case result := <-ic.ResultChannel:
          if result != nil {
            storeHttpResult(result, jobs, last)
          }
        default:
          break MoreResults
        }
      }
    }
    invocation.DeregisterInvocation(ic)
  }
}

func runHttpJobs(jobs []*Job, listenerPort string) {
  for _, job := range jobs {
    job.lock.Lock()
    job.jobRun.running = true
    job.lock.Unlock()
  }
  jobsToRun := jobs
  for {
    targetNames := []string{}
    delay := 10 * time.Millisecond
    isLastRound := true
    for i, job := range jobsToRun {
      job.lock.RLock()
      job.jobRun.lock.Lock()
      select {
      case <-job.jobRun.stopChannel:
        job.jobRun.running = false
      default:
      }
      if !job.jobRun.running || job.resultCount >= job.Count {
        jobsToRun = append(jobsToRun[:i], jobsToRun[i+1:]...)
      } else {
        targetNames = append(targetNames, job.httpTask.Name)
        if job.delayD > delay {
          delay = job.delayD
        }
        if job.Count > job.resultCount+1 {
          isLastRound = false
        }
      }
      job.jobRun.lock.Unlock()
      job.lock.RUnlock()
    }
    if len(targetNames) > 0 {
      log.Printf("Starting HTTP jobs: %+v\n", targetNames)
      invokeHttpTargets(jobsToRun, isLastRound)
    } else {
      break
    }
    time.Sleep(delay)
  }
  for _, job := range jobs {
    job.lock.Lock()
    job.jobRun.lock.Lock()
    job.jobResults[len(job.jobResults)-1].Finished = true
    job.jobRun.doneChannel <- true
    close(job.jobRun.stopChannel)
    close(job.jobRun.doneChannel)
    job.jobRun.lock.Unlock()
    job.jobRun = nil
    job.lock.Unlock()
  }
}

func (pj *PortJobs) runJobs(jobs []*Job) {
  httpJobs := []*Job{}
  cmdJobs := []*Job{}
  pj.lock.Lock()
  pj.jobRunCounter++
  for _, job := range jobs {
    if initJobRun(job, pj.jobRunCounter) {
      if job.httpTask != nil {
        httpJobs = append(httpJobs, job)
      } else {
        cmdJobs = append(cmdJobs, job)
      }
    }
  }
  pj.lock.Unlock()
  if len(cmdJobs) > 0 {
    go runCommandJobs(cmdJobs)
  }
  if len(httpJobs) > 0 {
    go runHttpJobs(httpJobs, pj.listenerPort)
  }
}

func (pj *PortJobs) stopJob(j string) bool {
  pj.lock.Lock()
  defer pj.lock.Unlock()
  job := pj.jobs[j]
  if job == nil || job.jobRun == nil {
    return false
  }
  done := false
  job.jobRun.lock.Lock()
  select {
  case done = <-job.jobRun.doneChannel:
  default:
  }
  if !done {
    job.jobRun.stopChannel <- true
  }
  job.jobRun.lock.Unlock()
  job.lock.Lock()
  job.jobRun = nil
  job.lock.Unlock()
  return true
}

func (pj *PortJobs) stopJobs(jobs []string) {
  for _, job := range jobs {
    pj.stopJob(job)
  }
}

func GetPortJobs(port string) *PortJobs {
  portJobsLock.Lock()
  defer portJobsLock.Unlock()
  pj := portJobs[port]
  if pj == nil {
    pj = &PortJobs{listenerPort: port}
    pj.init()
    portJobs[port] = pj
  }
  return pj
}

func getPortJobs(r *http.Request) *PortJobs {
  return GetPortJobs(util.GetListenerPort(r))
}

func RunJobs(jobs []string, port string) {
  pj := GetPortJobs(port)
  pj.runJobs(pj.getJobsToRun(jobs))
}

func StopJob(job string, port string) bool {
  pj := GetPortJobs(port)
  return pj.stopJob(job)
}

func StopJobs(jobs []string, port string) {
  for _, job := range jobs {
    StopJob(job, port)
  }
}

func addJob(w http.ResponseWriter, r *http.Request) {
  msg := ""
  if job, err := ParseJob(r); err == nil {
    pj := getPortJobs(r)
    pj.AddJob(job)
    msg = fmt.Sprintf("Added Job: %s\n", util.ToJSON(job))
    w.WriteHeader(http.StatusOK)
  } else {
    w.WriteHeader(http.StatusBadRequest)
    msg = fmt.Sprintf("Failed to add job with error: %s\n", err.Error())
  }
  fmt.Fprintln(w, msg)
  util.AddLogMessage(msg, r)
}

func removeJob(w http.ResponseWriter, r *http.Request) {
  msg := ""
  if jobs, present := util.GetListParam(r, "jobs"); present {
    getPortJobs(r).removeJobs(jobs)
    w.WriteHeader(http.StatusOK)
    msg = fmt.Sprintf("Jobs Removed: %s\n", jobs)
  } else {
    w.WriteHeader(http.StatusBadRequest)
    msg = "No jobs"
  }
  fmt.Fprintln(w, msg)
  util.AddLogMessage(msg, r)
}

func clearJobs(w http.ResponseWriter, r *http.Request) {
  getPortJobs(r).init()
  w.WriteHeader(http.StatusOK)
  msg := "Jobs cleared"
  fmt.Fprintln(w, msg)
  util.AddLogMessage(msg, r)
}

func getJobs(w http.ResponseWriter, r *http.Request) {
  util.AddLogMessage("Reporting jobs", r)
  util.WriteJsonPayload(w, getPortJobs(r).jobs)
}

func runJobs(w http.ResponseWriter, r *http.Request) {
  msg := ""
  pj := getPortJobs(r)
  names, _ := util.GetListParam(r, "jobs")
  jobsToRun := pj.getJobsToRun(names)
  if len(jobsToRun) > 0 {
    pj.runJobs(jobsToRun)
    w.WriteHeader(http.StatusOK)
    msg = fmt.Sprintf("Jobs %+v started\n", names)
  } else {
    w.WriteHeader(http.StatusNotModified)
    msg = "No jobs to run"
  }
  fmt.Fprintln(w, msg)
  util.AddLogMessage(msg, r)
}

func stopJobs(w http.ResponseWriter, r *http.Request) {
  msg := ""
  if jobs, present := util.GetListParam(r, "jobs"); present {
    getPortJobs(r).stopJobs(jobs)
    w.WriteHeader(http.StatusOK)
    msg = fmt.Sprintf("Jobs %+v stopped\n", jobs)
  } else {
    w.WriteHeader(http.StatusNotModified)
    msg = "No jobs to stop"
  }
  fmt.Fprintln(w, msg)
  util.AddLogMessage(msg, r)
}

func getJobResults(w http.ResponseWriter, r *http.Request) {
  msg := ""
  if job, present := util.GetStringParam(r, "job"); present {
    msg = fmt.Sprintf(util.ToJSON(getPortJobs(r).getJobResults(job)))
  } else {
    msg = "[]"
  }
  w.WriteHeader(http.StatusOK)
  fmt.Fprintln(w, msg)
  util.AddLogMessage(msg, r)
}

func ParseJobFromPayload(payload string) (*Job, error) {
  job := &Job{}
  if err := util.ReadJson(payload, job); err == nil {
    if job.Task != nil {
      var httpTask HttpJobTask
      var commandTask CommandJobTask
      var httpTaskError error
      var cmdTaskError error
      task := util.ToJSON(job.Task)
      if httpTaskError = util.ReadJson(task, &httpTask); httpTaskError == nil {
        if httpTaskError = invocation.ValidateSpec(&httpTask.InvocationSpec); httpTaskError == nil {
          job.httpTask = &httpTask
        }
      }
      if httpTaskError != nil {
        if cmdTaskError = util.ReadJson(task, &commandTask); cmdTaskError == nil {
          if commandTask.Cmd != "" {
            job.commandTask = &commandTask
          } else {
            cmdTaskError = errors.New("Missing command in command task")
          }
        }
      }
      if httpTaskError == nil || cmdTaskError == nil {
        return job, nil
      } else {
        msg := ""
        if cmdTaskError != nil {
          msg += "Command Task Error: [" + cmdTaskError.Error() + "] "
        }
        if httpTaskError != nil {
          msg = "HTTP Task Error: [" + httpTaskError.Error() + "] "
        }
        err := errors.New(msg)
        return job, err
      }
    } else {
      return nil, fmt.Errorf("Invalid Task: %s", err.Error())
    }
  } else {
    return nil, fmt.Errorf("Failed to parse json with error: %s", err.Error())
  }
}

func ParseJob(r *http.Request) (*Job, error) {
  return ParseJobFromPayload(util.Read(r.Body))
}
