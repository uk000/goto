package job

import (
	"bufio"
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
  Cmd     string   `json:"cmd"`
  Args    []string `json:"args"`
  fillers map[int]int
}

type HttpJobTask struct {
  invocation.InvocationSpec
  ParseJSON bool `json:"parseJSON"`
}

type JobResult struct {
  Index      string
  Finished   bool
  ResultTime time.Time
  Data       interface{}
}

type JobRun struct {
  index        int
  running      bool
  stopChannel  chan bool
  doneChannel  chan bool
  resultCount  int
  preparedArgs []string
  jobResults   []*JobResult
  lock         sync.RWMutex
}

type Job struct {
  ID            string        `json:"id"`
  Task          interface{}   `json:"task"`
  Auto          bool          `json:"auto"`
  Delay         string        `json:"delay"`
  Count         int           `json:"count"`
  MaxResults    int           `json:"maxResults"`
  KeepResults   int           `json:"keepResults"`
  KeepFirst     bool          `json:"keepFirst"`
  Timeout       time.Duration `json:"timeout"`
  OutputTrigger string        `json:"outputTrigger"`
  FinishTrigger string        `json:"finishTrigger"`
  delayD        time.Duration
  httpTask      *HttpJobTask
  commandTask   *CommandJobTask
  jobRunCounter int
  lock          sync.RWMutex
}

type PortJobs struct {
  jobs         map[string]*Job
  jobRuns      map[string]map[int]*JobRun
  listenerPort string
  lock         sync.RWMutex
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
  pj.jobRuns = map[string]map[int]*JobRun{}
}

func (pj *PortJobs) AddJob(job *Job) {
  pj.lock.Lock()
  defer pj.lock.Unlock()
  pj.jobs[job.ID] = job
  if job.Auto {
    go func() {
      log.Printf("Auto-invoking Job: %s\n", job.ID)
      pj.runJob(job)
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
  results := []interface{}{}

  pj.lock.RLock()
  job := pj.jobs[name]
  jobRuns := pj.jobRuns[name]
  pj.lock.RUnlock()

  if job != nil && jobRuns != nil {
    for _, jobRun := range jobRuns {
      jobRun.lock.RLock()
      for _, r := range jobRun.jobResults {
        results = append(results, r)
      }
      jobRun.lock.RUnlock()
    }
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

func storeJobResultsInRegistryLocker(job *Job, jobRun *JobRun) {
  if global.UseLocker && global.RegistryURL != "" {
    key := "job_" + job.ID + "_" + strconv.Itoa(jobRun.index)
    url := global.RegistryURL + "/registry/peers/" + global.PeerName + "/locker/store/" + key
    if resp, err := http.Post(url, "application/json",
      strings.NewReader(util.ToJSON(jobRun.jobResults))); err == nil {
      defer resp.Body.Close()
    }
  }
}

func storeCommandResult(data string, job *Job, jobRun *JobRun, last bool) {
  job.lock.Lock()
  defer job.lock.Unlock()
  jobRun.resultCount++
  index := strconv.Itoa(jobRun.index) + "." + strconv.Itoa(jobRun.resultCount)
  jobResult := &JobResult{Index: index, Finished: last, ResultTime: time.Now(), Data: data}
  jobRun.jobResults = append(jobRun.jobResults, jobResult)
  if len(jobRun.jobResults) > job.KeepResults {
    if job.KeepFirst {
      jobRun.jobResults = append(jobRun.jobResults[:1], jobRun.jobResults[2:]...)
    } else {
      jobRun.jobResults = jobRun.jobResults[1:]
    }
  }
  storeJobResultsInRegistryLocker(job, jobRun)
}

func (pj *PortJobs) runJobWithInput(jobName string, input string) {
  pj.lock.RLock()
  job := pj.jobs[jobName]
  pj.lock.RUnlock()
  if job == nil {
    return
  }
  if job.commandTask != nil {
    pj.runCommandWithInput(job, input)
  } else if job.httpTask != nil {
    pj.runHttpJobWithInput(job, input)
  }
}

func (pj *PortJobs) runCommandWithInput(job *Job, input string) {
  job.lock.Lock()
  args := []string{}
  for _, a := range job.commandTask.Args {
    args = append(args, a)
  }
  if len(job.commandTask.fillers) > 0 {
    inputs := strings.Split(input, " ")
    for f := range job.commandTask.fillers {
      if len(inputs) >= f {
        for a := range args {
          args[a] = strings.ReplaceAll(args[a], util.GetFillerMarker(strconv.Itoa(f)), inputs[f-1])
        }
      }
    }
  }
  job.lock.Unlock()
  jobRun := pj.initJobRun(job)
  if jobRun == nil {
    return
  }
  jobRun.preparedArgs = args
  pj.executeJobRun(job, jobRun)
}

func (pj *PortJobs) runCommandAndStoreResult(job *Job, jobRun *JobRun, last bool) {
  runLlock := sync.Mutex{}
  job.lock.RLock()
  realCmd := strings.Join(jobRun.preparedArgs, " ")
  cmd := exec.Command(job.commandTask.Cmd, jobRun.preparedArgs...)
  job.lock.RUnlock()
  stdout, err1 := cmd.StdoutPipe()
  stderr, err2 := cmd.StderrPipe()
  if err1 != nil || err2 != nil {
    log.Printf("Failed to open output stream from command: %s\n", realCmd)
    return
  }
  outScanner := bufio.NewScanner(stdout)
  errScanner := bufio.NewScanner(stderr)

  if err := cmd.Start(); err != nil {
    msg := fmt.Sprintf("Failed to execute command [%s] with error [%s]", realCmd, err.Error())
    log.Println(msg)
    storeCommandResult(msg, job, jobRun, last)
    return
  }
  outputChannel := make(chan string)
  finishChannel := make(chan bool)
  stop := false
  readOutput := func(scanner *bufio.Scanner) {
    for scanner.Scan() {
      runLlock.Lock()
      if !stop {
        out := scanner.Text()
        if len(out) > 0 {
          outputChannel <- out
        }
      }
      runLlock.Unlock()
      if stop {
        break
      }
    }
  }
  go func() {
    wg := sync.WaitGroup{}
    wg.Add(1)
    go func() {
      readOutput(outScanner)
      wg.Done()
    }()
    wg.Add(1)
    go func() {
      readOutput(errScanner)
      wg.Done()
    }()
    wg.Wait()
    runLlock.Lock()
    if !stop {
      jobRun.doneChannel <- true
    }
    runLlock.Unlock()
    close(outputChannel)
    finishChannel <- true
  }()
  stopCommand := func() {
    runLlock.Lock()
    stop = true
    jobRun.lock.Lock()
    jobRun.running = false
    jobRun.lock.Unlock()
    if err := cmd.Process.Kill(); err != nil {
      log.Printf("Failed to stop command [%s] with error [%s]\n", job.commandTask.Cmd, err.Error())
    }
    runLlock.Unlock()
  }
  var stopChannel chan bool
  job.lock.RLock()
  stopChannel = jobRun.stopChannel
  job.lock.RUnlock()
  done := false
  resultCount := 0
  go func() {
  Done:
    for {
      select {
      case <-time.After(job.Timeout):
        if !done {
          stopCommand()
        }
        break Done
      case <-stopChannel:
        if !done {
          stopCommand()
        }
        break Done
      case done = <-jobRun.doneChannel:
        if last {
          storeCommandResult("", job, jobRun, true)
        }
        break Done
      case out := <-outputChannel:
        if resultCount < job.MaxResults {
          if out != "" {
            resultCount++
            storeCommandResult(out, job, jobRun, done && last)
            if job.OutputTrigger != "" {
              go pj.runJobWithInput(job.OutputTrigger, out)
            }
          }
        } else {
          stopCommand()
        }
      }
    }
  }()
  cmd.Wait()
  <-finishChannel
  done = true
}

func storeHttpResult(result *invocation.InvocationResult, job *Job, jobRun *JobRun, last bool) {
  job.lock.Lock()
  if job.httpTask != nil && job.httpTask.Name == result.TargetName {
    jobRun.resultCount++
    index := strconv.Itoa(jobRun.index) + "." + strconv.Itoa(jobRun.resultCount)
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
    jobRun.jobResults = append(jobRun.jobResults, jobResult)
    if len(jobRun.jobResults) > job.KeepResults {
      if job.KeepFirst {
        jobRun.jobResults = append(jobRun.jobResults[:1], jobRun.jobResults[2:]...)
      } else {
        jobRun.jobResults = jobRun.jobResults[1:]
      }
    }
  }
  storeJobResultsInRegistryLocker(job, jobRun)
  job.lock.Unlock()
}

func (pj *PortJobs) invokeHttpTarget(job *Job, jobRun *JobRun, last bool) {
  job.lock.RLock()
  target := &job.httpTask.InvocationSpec
  targets := []*invocation.InvocationSpec{target}
  job.lock.RUnlock()
  ic := invocation.RegisterInvocation(jobRun.index)
  finishChannel := make(chan bool)
  go func() {
    invocation.InvokeTargets(targets, ic, true)
    finishChannel <- true
  }()
  done := false
  resultCount := 0
Done:
  for {
    select {
    case <-time.After(job.Timeout):
      ic.StopChannel <- target.Name
      break Done
    case done = <-ic.DoneChannel:
      break Done
    case <-jobRun.stopChannel:
      ic.StopChannel <- target.Name
      break Done
    case result := <-ic.ResultChannel:
      if result != nil {
        if resultCount < job.MaxResults {
          resultCount++
          storeHttpResult(result, job, jobRun, last)
          if job.OutputTrigger != "" {
            pj.runJobWithInput(job.OutputTrigger, "")
          }
        } else {
          ic.StopChannel <- target.Name
        }
      }
    }
  }
  <-finishChannel
  jobRun.lock.Lock()
  jobRun.running = false
  jobRun.lock.Unlock()

  if done {
  MoreResults:
    for {
      select {
      case result := <-ic.ResultChannel:
        if result != nil {
          if resultCount <= job.MaxResults {
            storeHttpResult(result, job, jobRun, last)
            if job.OutputTrigger != "" {
              pj.runJobWithInput(job.OutputTrigger, "")
            }
          }
        }
      default:
        break MoreResults
      }
    }
  }
  invocation.DeregisterInvocation(ic)
}

func (pj *PortJobs) runHttpJobWithInput(job *Job, input string) {
  pj.runJob(job)
}

func (pj *PortJobs) executeJobRun(job *Job, jobRun *JobRun) {
  job.lock.Lock()
  log.Printf("job [%s] Run [%d] Started \n", job.ID, jobRun.index)
  id := job.ID
  count := job.Count
  delay := job.delayD
  finishTrigger := job.FinishTrigger
  if job.commandTask != nil && jobRun.preparedArgs == nil {
    jobRun.preparedArgs = job.commandTask.Args
  }
  job.lock.Unlock()

  jobRun.lock.Lock()
  jobRun.running = true
  jobRun.lock.Unlock()

  for i := 0; i < count; i++ {
    time.Sleep(delay)
    if job.commandTask != nil {
      pj.runCommandAndStoreResult(job, jobRun, i == count-1)
    } else if job.httpTask != nil {
      pj.invokeHttpTarget(job, jobRun, i == count-1)
    }
    jobRun.lock.RLock()
    running := jobRun.running
    jobRun.lock.RUnlock()
    if !running {
      break
    }
  }
  job.lock.Lock()
  jobRun.lock.Lock()
  close(jobRun.stopChannel)
  close(jobRun.doneChannel)
  log.Printf("job [%s] Run [%d] Finished \n", id, jobRun.index)
  jobRun.lock.Unlock()
  job.lock.Unlock()

  if finishTrigger != "" {
    pj.runJobWithInput(finishTrigger, "")
  }
}

func (pj *PortJobs) initJobRun(job *Job) *JobRun {
  if job == nil || (job.commandTask == nil && job.httpTask == nil) {
    return nil
  }
  pj.lock.Lock()
  defer pj.lock.Unlock()
  job.lock.Lock()
  defer job.lock.Unlock()
  job.jobRunCounter++
  jobRun := &JobRun{}
  jobRun.jobResults = []*JobResult{}
  jobRun.index = job.jobRunCounter
  jobRun.stopChannel = make(chan bool)
  jobRun.doneChannel = make(chan bool, 10)
  if pj.jobRuns[job.ID] == nil {
    pj.jobRuns[job.ID] = map[int]*JobRun{}
  }
  pj.jobRuns[job.ID][job.jobRunCounter] = jobRun
  return jobRun
}

func (pj *PortJobs) runJob(job *Job) {
  jobRun := pj.initJobRun(job)
  if jobRun == nil {
    return
  }
  pj.executeJobRun(job, jobRun)
}

func (pj *PortJobs) runJobs(jobs []*Job) {
  for _, job := range jobs {
    pj.runJob(job)
  }
}

func (pj *PortJobs) stopJob(j string) bool {
  pj.lock.Lock()
  defer pj.lock.Unlock()
  job := pj.jobs[j]
  jobRuns := pj.jobRuns[j]
  if job == nil || jobRuns == nil {
    return false
  }
  done := false
  for _, jobRun := range jobRuns {
    jobRun.lock.Lock()
    select {
    case done = <-jobRun.doneChannel:
    default:
    }
    if !done {
      jobRun.stopChannel <- true
    }
    jobRun.lock.Unlock()
  }
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
    w.WriteHeader(http.StatusNotAcceptable)
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
    w.WriteHeader(http.StatusNotAcceptable)
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
    job.delayD, _ = time.ParseDuration(job.Delay)
    if job.Count == 0 {
      job.Count = 1
    }
    if job.KeepResults <= 0 {
      job.KeepResults = 5
    }
    if job.MaxResults <= 0 {
      job.MaxResults = 20
    }
    if job.Timeout <= 0 {
      job.Timeout = 10 * time.Minute
    }
    if job.Task != nil {
      var httpTask HttpJobTask
      commandTask := CommandJobTask{fillers: map[int]int{}}
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
            for _, arg := range commandTask.Args {
              for i := 1; i <= 9; i++ {
                if strings.Contains(arg, util.GetFillerMarker(strconv.Itoa(i))) {
                  commandTask.fillers[i]++
                }
              }
              job.commandTask = &commandTask
            }
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
