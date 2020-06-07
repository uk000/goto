package jobrunner

import (
	"bytes"
	"fmt"
	"goto/pkg/http/client/target"
	"goto/pkg/http/invocation"
	"goto/pkg/job/jobtypes"
	"goto/pkg/util"
	"log"
	"net/http"
	"os/exec"
	"strconv"
	"sync"
	"time"

	"github.com/gorilla/mux"
)

type PortJobs struct {
  jobs          map[string]*jobtypes.Job
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
  pj.jobs = map[string]*jobtypes.Job{}
}

func (pj *PortJobs) AddJob(job *jobtypes.Job) {
  pj.lock.Lock()
  defer pj.lock.Unlock()
  job.DelayD, _ = time.ParseDuration(job.Delay)
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
      pj.runJobs([]*jobtypes.Job{job})
    }()
  }
}

func (pj *PortJobs) removeJobs(jobs []string) {
  pj.lock.Lock()
  defer pj.lock.Unlock()
  for _, j := range jobs {
    job := pj.jobs[j]
    if job != nil {
      job.Lock.Lock()
      defer job.Lock.Unlock()
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
    for _, r := range job.JobResults {
      results = append(results, r)
    }
  }
  return results
}

func (pj *PortJobs) getJobsToRun(names []string) []*jobtypes.Job {
  pj.lock.RLock()
  defer pj.lock.RUnlock()
  var jobsToRun []*jobtypes.Job
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

func runCommandAndStoreResult(job *jobtypes.Job) {
  runLlock := sync.Mutex{}
  cmd := exec.Command(job.CommandTask.Cmd, job.CommandTask.Args...)
  var stdout bytes.Buffer
  var stderr bytes.Buffer
  cmd.Stdout = &stdout
  cmd.Stderr = &stderr
  if err := cmd.Run(); err != nil {
    log.Printf("Failed to execute command [%s] with error [%s]\n", job.CommandTask.Cmd, err.Error())
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
  select {
  case <-job.JobRun.StopChannel:
    runLlock.Lock()
    stop = true
    job.JobRun.Running = false
    if err := cmd.Process.Kill(); err != nil {
      log.Printf("Failed to stop command [%s] with error [%s]\n", job.CommandTask.Cmd, err.Error())
    }
    runLlock.Unlock()
  case out := <-c:
    job.ResultCount++
    index := strconv.Itoa(job.JobRun.Index)+"."+strconv.Itoa(job.ResultCount)
    jobResult := &jobtypes.JobResult{Index: index, Finished: !job.JobRun.Running, Data: out}
    job.JobResults = append(job.JobResults, jobResult)
    if len(job.JobResults) > job.MaxResults {
      if job.KeepFirst {
        job.JobResults = append(job.JobResults[:1], job.JobResults[2:]...)
      } else {
        job.JobResults = job.JobResults[1:]
      }
    }
  }
}

func initJobRun(job *jobtypes.Job, index int) bool {
  if job == nil || job.JobRun != nil || (job.CommandTask == nil && job.HttpTask == nil) {
    return false
  }
  job.Lock.Lock()
  defer job.Lock.Unlock()
  job.JobResults = []*jobtypes.JobResult{}
  job.ResultCount = 0
  job.JobRun = &jobtypes.JobRunInfo{}
  job.JobRun.Index = index
  job.JobRun.StopChannel = make(chan bool)
  job.JobRun.DoneChannel = make(chan bool, 10)
  return true
}

func runCommandJob(job *jobtypes.Job) {
  log.Printf("Starting Command job: %s\n", job.ID)
  jobRun := job.JobRun
  job.Lock.Lock()
  defer job.Lock.Unlock()
  jobRun.Running = true
  for i := 0; i < job.Count; i++ {
    runCommandAndStoreResult(job)
    if !jobRun.Running {
      break
    }
    time.Sleep(job.DelayD)
  }
  jobRun.Lock.Lock()
  job.JobResults[len(job.JobResults)-1].Finished = true
  jobRun.DoneChannel <- true
  close(jobRun.StopChannel)
  close(jobRun.DoneChannel)
  jobRun.Lock.Unlock()
  job.JobRun = nil
  log.Printf("job [%s] Finished \n", job.ID)
}

func runCommandJobs(jobs []*jobtypes.Job) {
  for _, job := range jobs {
    go runCommandJob(job)
  }
}

func storeHttpResult(result *invocation.InvocationResult, jobs []*jobtypes.Job) {
  for _, job := range jobs {
    if job.HttpTask != nil && job.HttpTask.Name == result.TargetName {
      job.ResultCount++
      index := strconv.Itoa(job.JobRun.Index)+"."+strconv.Itoa(job.ResultCount)
      jobResult := &jobtypes.JobResult{Index: index, Finished: !job.JobRun.Running}
      if job.HttpTask.ParseJSON {
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
      job.JobResults = append(job.JobResults, jobResult)
      if len(job.JobResults) > job.MaxResults {
        if job.KeepFirst {
          job.JobResults = append(job.JobResults[:1], job.JobResults[2:]...)
        } else {
          job.JobResults = job.JobResults[1:]
        }
      }
    }
  }
}

func invokeHttpTargets(pc *target.PortClient, jobs []*jobtypes.Job) {
  targets := []*invocation.InvocationSpec{}
  for _, job := range jobs {
    targets = append(targets, &job.HttpTask.InvocationSpec)
  }
  if len(targets) > 0 {
    ic := pc.RegisterInvocation()
    c := make(chan bool)
    go func() {
      invocation.InvokeTargets(targets, ic, true)
      pc.DeregisterInvocation(ic)
      c <- true
    }()
    Results:
    for {
      select {
      case <-ic.DoneChannel:
        break Results
      case <-ic.StopChannel:
        break Results
      case result := <-ic.ResultChannel:
        if result != nil {
          storeHttpResult(result, jobs)
        }
      }
    }
    <-c    
  }
}

func runHttpJobs(jobs []*jobtypes.Job, listenerPort string) {
  pc := target.GetClientForPort(listenerPort)
  for _, job := range jobs {
    job.Lock.Lock()
    job.JobRun.Running = true
  }
  jobsToRun := jobs
  for {
    targetNames := []string{}
    delay := 10 * time.Millisecond
    for i, job := range jobsToRun {
      select {
      case <-job.JobRun.StopChannel:
        job.JobRun.Running = false
      default:
      }
      if !job.JobRun.Running || job.ResultCount >= job.Count {
        jobsToRun = append(jobsToRun[:i], jobsToRun[i+1:]...)
      } else {
        targetNames = append(targetNames, job.HttpTask.Name)
        if job.DelayD > delay {
          delay = job.DelayD
        }
      }
    }
    if len(targetNames) > 0 {
      log.Printf("Starting HTTP jobs: %+v\n", targetNames)
      invokeHttpTargets(pc, jobsToRun)
    } else {
      break
    }
  }
  for _, job := range jobs {
    job.JobResults[len(job.JobResults)-1].Finished = true
    job.JobRun.DoneChannel <- true
    close(job.JobRun.StopChannel)
    close(job.JobRun.DoneChannel)
    job.JobRun = nil
    job.Lock.Unlock()
  }
}

func (pj *PortJobs) runJobs(jobs []*jobtypes.Job) {
  httpJobs := []*jobtypes.Job{}
  cmdJobs := []*jobtypes.Job{}
  pj.lock.Lock()
  pj.jobRunCounter++
  for _, job := range jobs {
    if initJobRun(job, pj.jobRunCounter) {
      if job.HttpTask != nil {
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
  if job == nil || job.JobRun == nil {
    return false
  }
  done := false
  job.JobRun.Lock.Lock()
  select {
  case done = <-job.JobRun.DoneChannel:
  default:
  }
  if !done {
    job.JobRun.StopChannel <- true
  }
  job.JobRun.Lock.Unlock()
  job.Lock.Lock()
  job.JobRun = nil
  job.Lock.Unlock()
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
  if job, err := jobtypes.ParseJob(r); err == nil {
    pj := getPortJobs(r)
    pj.AddJob(job)
    log.Printf("Added Job: %+v\n", job)
    fmt.Fprintf(w, "Added Job: %s\n", util.ToJSON(job))
    w.WriteHeader(http.StatusOK)
  } else {
    w.WriteHeader(http.StatusBadRequest)
    fmt.Fprintf(w, "Failed to add job with error: %s\n", err.Error())
    log.Printf("Failed to add job with error: %s\n", err.Error())
  }
}

func removeJob(w http.ResponseWriter, r *http.Request) {
  if jobs, present := util.GetListParam(r, "jobs"); present {
    getPortJobs(r).removeJobs(jobs)
    w.WriteHeader(http.StatusOK)
    fmt.Fprintf(w, "Jobs Removed: %s\n", jobs)
    log.Printf("Jobs Removed: %s\n", jobs)
  }
}

func clearJobs(w http.ResponseWriter, r *http.Request) {
  getPortJobs(r).init()
  w.WriteHeader(http.StatusOK)
  fmt.Fprintln(w, "Jobs cleared")
  log.Println("Jobs cleared")
}

func getJobs(w http.ResponseWriter, r *http.Request) {
  util.WriteJsonPayload(w, getPortJobs(r).jobs)
}

func runJobs(w http.ResponseWriter, r *http.Request) {
  pj := getPortJobs(r)
  names, _ := util.GetListParam(r, "jobs")
  jobsToRun := pj.getJobsToRun(names)
  if len(jobsToRun) > 0 {
    pj.runJobs(jobsToRun)
    w.WriteHeader(http.StatusOK)
    fmt.Fprintf(w, "Jobs %+v started\n", names)
  } else {
    w.WriteHeader(http.StatusOK)
    fmt.Fprintln(w, "No jobs to run")
  }
}

func stopJobs(w http.ResponseWriter, r *http.Request) {
  if jobs, present := util.GetListParam(r, "jobs"); present {
    getPortJobs(r).stopJobs(jobs)
    w.WriteHeader(http.StatusOK)
    fmt.Fprintf(w, "Jobs %+v stopped\n", jobs)
  } else {
    w.WriteHeader(http.StatusOK)
    fmt.Fprintln(w, "No jobs to stop")
  }
}

func getJobResults(w http.ResponseWriter, r *http.Request) {
  if job, present := util.GetStringParam(r, "job"); present {
    fmt.Fprintln(w, util.ToJSON(getPortJobs(r).getJobResults(job)))
  } else {
    fmt.Fprintln(w, "[]")
  }
  w.WriteHeader(http.StatusOK)
}
