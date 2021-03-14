package job

import (
  "bufio"
  "errors"
  "fmt"
  "goto/pkg/events"
  . "goto/pkg/events/eventslist"
  "goto/pkg/global"
  "goto/pkg/invocation"
  "goto/pkg/util"
  "io/ioutil"
  "log"
  "net/http"
  "os"
  "os/exec"
  "strconv"
  "strings"
  "sync"
  "time"

  "github.com/gorilla/mux"
)

type CommandJobTask struct {
  Cmd             string         `json:"cmd"`
  Args            []string       `json:"args"`
  OutputMarkers   map[int]string `json:"outputMarkers"`
  OutputSeparator string         `json:"outputSeparator"`
  fillers         map[string]int
}

type HttpJobTask struct {
  invocation.InvocationSpec
  ParseJSON bool `json:"parseJSON"`
}

type JobResult struct {
  ID         string      `json:"id"`
  Finished   bool        `json:"finished"`
  Stopped    bool        `json:"stopped"`
  Last       bool        `json:"last"`
  ResultTime time.Time   `json:"time"`
  Data       interface{} `json:"data"`
}

type JobRunContext struct {
  id          int
  finished    bool
  stopped     bool
  stopChannel chan bool
  doneChannel chan bool
  resultCount int
  jobArgs     []string
  jobResults  []*JobResult
  markers     map[string]string
  lock        sync.RWMutex
}

type Job struct {
  Name          string        `json:"name"`
  Task          interface{}   `json:"task"`
  Auto          bool          `json:"auto"`
  Delay         string        `json:"delay"`
  InitialDelay  string        `json:"initialDelay"`
  Count         int           `json:"count"`
  MaxResults    int           `json:"maxResults"`
  KeepResults   int           `json:"keepResults"`
  KeepFirst     bool          `json:"keepFirst"`
  Timeout       time.Duration `json:"timeout"`
  OutputTrigger string        `json:"outputTrigger"`
  FinishTrigger string        `json:"finishTrigger"`
  delayD        time.Duration
  initialDelayD time.Duration
  httpTask      *HttpJobTask
  commandTask   *CommandJobTask
  jobRunCounter int
  lock          sync.RWMutex
}

type JobManager struct {
  jobs    map[string]*Job
  jobRuns map[string]map[int]*JobRunContext
  lock    sync.RWMutex
}

var (
  Handler = util.ServerHandler{Name: "jobs", SetRoutes: SetRoutes}
  Jobs    = newJobManager()
)

func SetRoutes(r *mux.Router, parent *mux.Router, root *mux.Router) {
  jobsRouter := r.PathPrefix("/jobs").Subrouter()
  util.AddRoute(jobsRouter, "/add", addJob, "POST", "PUT")
  util.AddRoute(jobsRouter, "/add/script/{name}", storeJobScriptOrFile, "POST", "PUT")
  util.AddRouteQ(jobsRouter, "/store/file/{name}", storeJobScriptOrFile, "path", "{path}", "POST", "PUT")
  util.AddRoute(jobsRouter, "/store/file/{name}", storeJobScriptOrFile, "POST", "PUT")
  util.AddRoute(jobsRouter, "/{jobs}/remove", removeJob, "POST")
  util.AddRoute(jobsRouter, "/clear", clearJobs, "POST")
  util.AddRoute(jobsRouter, "/{jobs}/run", runJobs, "POST")
  util.AddRoute(jobsRouter, "/run/all", runJobs, "POST")
  util.AddRoute(jobsRouter, "/{jobs}/stop", stopJobs, "POST")
  util.AddRoute(jobsRouter, "/stop/all", stopJobs, "POST")
  util.AddRoute(jobsRouter, "/{job}/results", getJobResults, "GET")
  util.AddRoute(jobsRouter, "/results", getJobResults, "GET")
  util.AddRoute(jobsRouter, "/results/clear", clearJobResults, "POST")
  util.AddRoute(jobsRouter, "", getJobs, "GET")
}

func newJobManager() *JobManager {
  j := &JobManager{}
  j.init()
  return j
}

func (jm *JobManager) init() {
  jm.lock.Lock()
  defer jm.lock.Unlock()
  jm.jobs = map[string]*Job{}
  jm.jobRuns = map[string]map[int]*JobRunContext{}
}

func (jm *JobManager) AddJob(job *Job) {
  jm.lock.Lock()
  defer jm.lock.Unlock()
  jm.jobs[job.Name] = job
  delete(jm.jobRuns, job.Name)
  if job.Auto {
    log.Printf("Auto-invoking Job: %s\n", job.Name)
    go jm.RunJob(job)
  }
}

func (jm *JobManager) StoreJobScriptOrFile(filePath, fileName string, content []byte, scriptJob bool) (string, bool) {
  if fileName != "" {
    if _, err := os.Stat(filePath); os.IsNotExist(err) {
      os.MkdirAll(filePath, os.ModePerm)
    }    
    filePath = util.BuildFilePath(filePath, fileName)
    if err := ioutil.WriteFile(filePath, content, 0777); err == nil {
      if scriptJob {
        task := &CommandJobTask{Cmd: filePath}
        job := &Job{Name: fileName, Task: task, commandTask: task, Count: 1, KeepResults: 3}
        Jobs.AddJob(job)
      }
      return filePath, true
    } else {
      fmt.Printf("Failed to store job file [%s] with error: %s\n", filePath, err.Error())
    }
  }
  return "", false
}

func (jm *JobManager) removeJobs(jobs []string) {
  jm.lock.Lock()
  defer jm.lock.Unlock()
  for _, j := range jobs {
    job := jm.jobs[j]
    if job != nil {
      job.lock.Lock()
      defer job.lock.Unlock()
      delete(jm.jobs, j)
      delete(jm.jobRuns, j)
    }
  }
}

func (jm *JobManager) getJobResults(name string) map[int][]interface{} {
  results := map[int][]interface{}{}

  jm.lock.RLock()
  job := jm.jobs[name]
  jobRuns := jm.jobRuns[name]
  jm.lock.RUnlock()

  if job != nil && jobRuns != nil {
    for _, jobRun := range jobRuns {
      jobRun.lock.RLock()
      results[jobRun.id] = []interface{}{}
      for _, r := range jobRun.jobResults {
        results[jobRun.id] = append(results[jobRun.id], r)
      }
      jobRun.lock.RUnlock()
    }
  }
  return results
}

func (jm *JobManager) getAllJobsResults() map[string]map[int][]interface{} {
  results := map[string]map[int][]interface{}{}
  jm.lock.RLock()
  defer jm.lock.RUnlock()
  for _, job := range jm.jobs {
    job.lock.RLock()
    results[job.Name] = map[int][]interface{}{}
    for _, jobRun := range jm.jobRuns[job.Name] {
      jobRun.lock.RLock()
      results[job.Name][jobRun.id] = []interface{}{}
      for _, r := range jobRun.jobResults {
        results[job.Name][jobRun.id] = append(results[job.Name][jobRun.id], r)
      }
      jobRun.lock.RUnlock()
    }
    job.lock.RUnlock()
  }
  return results
}

func (jm *JobManager) getJobsToRun(names []string) ([]string, []*Job) {
  jm.lock.RLock()
  defer jm.lock.RUnlock()
  var jobsToRun []*Job
  if len(names) > 0 {
    for _, j := range names {
      if job, found := jm.jobs[j]; found {
        jobsToRun = append(jobsToRun, job)
      }
    }
  } else {
    if len(jm.jobs) > 0 {
      for _, job := range jm.jobs {
        jobsToRun = append(jobsToRun, job)
        names = append(names, job.Name)
      }
    }
  }
  return names, jobsToRun
}

func storeJobResultsInRegistryLocker(jobID string, runIndex int, jobResults []*JobResult) {
  if global.UseLocker && global.RegistryURL != "" {
    key := "job_" + jobID + "_" + strconv.Itoa(runIndex)
    url := global.RegistryURL + "/registry/peers/" + global.PeerName + "/" + global.PeerAddress + "/locker/store/" + key
    if resp, err := http.Post(url, "application/json",
      strings.NewReader(util.ToJSON(jobResults))); err == nil {
      util.CloseResponse(resp)
    }
  }
}

func storeJobResult(job *Job, jobRun *JobRunContext, iteration int, data interface{}, last bool) {
  job.lock.RLock()
  jobID := job.Name
  keepResults := job.KeepResults
  keepFirst := job.KeepFirst
  job.lock.RUnlock()
  jobRun.lock.Lock()
  defer jobRun.lock.Unlock()
  jobRun.resultCount++
  index := strconv.Itoa(jobRun.id) + "." + strconv.Itoa(iteration) + "." + strconv.Itoa(jobRun.resultCount)
  jobResult := &JobResult{ID: index, Last: last, Stopped: jobRun.stopped,
    Finished: jobRun.finished, ResultTime: time.Now(), Data: data}
  jobRun.jobResults = append(jobRun.jobResults, jobResult)
  if len(jobRun.jobResults) > keepResults {
    if keepFirst {
      jobRun.jobResults = append(jobRun.jobResults[:1], jobRun.jobResults[2:]...)
    } else {
      jobRun.jobResults = jobRun.jobResults[1:]
    }
  }
  storeJobResultsInRegistryLocker(jobID, jobRun.id, jobRun.jobResults)

}

func (jm *JobManager) runCommandJob(job *Job, jobRun *JobRunContext, iteration int, last bool) {
  job.lock.RLock()
  jobRun.lock.RLock()
  commandTask := job.commandTask
  realCmd := strings.Join(jobRun.jobArgs, " ")
  cmd := exec.Command(commandTask.Cmd, jobRun.jobArgs...)
  outputTrigger := job.OutputTrigger
  maxResults := job.MaxResults
  stopChannel := jobRun.stopChannel
  doneChannel := jobRun.doneChannel
  jobRun.lock.RUnlock()
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
    storeJobResult(job, jobRun, iteration, msg, last)
    return
  }
  outputChannel := make(chan string)
  stop := false
  resultCount := 0

  readOutput := func(scanner *bufio.Scanner) {
    for scanner.Scan() {
      if !stop {
        out := scanner.Text()
        if len(out) > 0 {
          outputChannel <- out
        }
      }
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
    close(outputChannel)
    doneChannel <- true
  }()

  stopCommand := func() {
    stop = true
    jobRun.lock.Lock()
    jobRun.stopped = true
    jobRun.lock.Unlock()
    if err := cmd.Process.Kill(); err != nil {
      log.Printf("Failed to stop command [%s] with error [%s]\n", job.commandTask.Cmd, err.Error())
    }
  }

Done:
  for {
    select {
    case <-time.After(job.Timeout):
      if job.Timeout > 0 {
        stopCommand()
        break Done
      }
    case <-stopChannel:
      stopCommand()
      break Done
    case <-doneChannel:
      break Done
    case out := <-outputChannel:
      if maxResults == 0 || resultCount < maxResults {
        if out != "" {
          resultCount++
          storeJobResult(job, jobRun, iteration, out, last)
          if outputTrigger != "" {
            if markers := prepareMarkers(out, commandTask, jobRun); len(markers) > 0 {
              go jm.runJobWithInput(outputTrigger, markers)
            }
          }
        }
      } else {
        stopCommand()
      }
    }
  }
  cmd.Wait()
  if last {
    jobRun.lock.Lock()
    jobRun.finished = true
    jobRun.lock.Unlock()
    storeJobResult(job, jobRun, iteration, "", last)
  }
}

func storeHttpResult(result *invocation.InvocationResult, job *Job, iteration int, jobRun *JobRunContext, last bool) {
  job.lock.RLock()
  httpTask := job.httpTask
  job.lock.RUnlock()
  if httpTask == nil || httpTask.Name != result.TargetName {
    return
  }
  var data interface{}
  if httpTask.ParseJSON {
    json := map[string]interface{}{}
    if err := util.ReadJson(result.Body, &json); err != nil {
      log.Printf("Failed reading response JSON: %s\n", err.Error())
      data = result.Body
    } else {
      data = json
    }
  } else {
    data = result.Body
  }
  storeJobResult(job, jobRun, iteration, data, last)
}

func (jm *JobManager) invokeHttpTarget(job *Job, jobRun *JobRunContext, iteration int, last bool) {
  job.lock.RLock()
  target := &job.httpTask.InvocationSpec
  outputTrigger := job.OutputTrigger
  maxResults := job.MaxResults
  job.lock.RUnlock()
  jobRun.lock.RLock()
  stopChannel := jobRun.stopChannel
  doneChannel := jobRun.doneChannel
  jobRun.lock.RUnlock()

  resultCount := 0
  tracker := invocation.RegisterInvocation(target)

  go func() {
    invocation.StartInvocation(tracker)
    doneChannel <- true
  }()

  sendStopSignal := func() {
    tracker.StopChannel <- true
    jobRun.lock.Lock()
    jobRun.stopped = true
    jobRun.lock.Unlock()
  }

  storeResult := func(result *invocation.InvocationResult) bool {
    if resultCount < maxResults {
      if result != nil {
        resultCount++
        storeHttpResult(result, job, iteration, jobRun, last)
        if outputTrigger != "" {
          jm.runJobWithInput(outputTrigger, nil)
        }
        return true
      }
    }
    return false
  }

Done:
  for {
    select {
    case <-time.After(job.Timeout):
      sendStopSignal()
      break Done
    case <-stopChannel:
      sendStopSignal()
      break Done
    case <-doneChannel:
      break Done
    case result := <-tracker.ResultChannel:
      if !storeResult(result) {
        sendStopSignal()
      }
    }
  }
  jobRun.lock.Lock()
  jobRun.finished = true
  jobRun.lock.Unlock()
  for result := range tracker.ResultChannel {
    if !storeResult(result) {
      break
    }
  }
}

func prepareMarkers(output string, sourceCommand *CommandJobTask, jobRun *JobRunContext) map[string]string {
  markers := map[string]string{}
  outputMarkers := sourceCommand.OutputMarkers
  separator := sourceCommand.OutputSeparator
  if len(outputMarkers) > 0 {
    if separator == "" {
      separator = " "
    }
    jobRun.lock.RLock()
    for k, v := range jobRun.markers {
      markers[k] = v
    }
    jobRun.lock.RUnlock()
    pieces := strings.Split(output, separator)
    for i, piece := range pieces {
      if outputMarkers[i+1] != "" {
        markers[util.GetFillerMarked(outputMarkers[i+1])] = piece
      }
    }
  }
  return markers
}

func (jm *JobManager) runJobWithInput(jobName string, markers map[string]string) {
  jm.lock.RLock()
  job := jm.jobs[jobName]
  jm.lock.RUnlock()
  if job == nil {
    return
  }
  if job.commandTask != nil {
    jm.runCommandWithInput(job, markers)
  } else if job.httpTask != nil {
    jm.runHttpJobWithInput(job, markers)
  }
}

func (jm *JobManager) runCommandWithInput(job *Job, markers map[string]string) {
  job.lock.Lock()
  args := []string{}
  for _, a := range job.commandTask.Args {
    args = append(args, a)
  }
  if markers != nil && len(markers) > 0 && len(job.commandTask.fillers) > 0 {
    for f := range job.commandTask.fillers {
      value := markers[f]
      if value != "" {
        for a := range args {
          args[a] = strings.ReplaceAll(args[a], f, value)
        }
      }
    }
  }
  job.lock.Unlock()
  jm.runJob(job, args, markers)
}

func (jm *JobManager) runHttpJobWithInput(job *Job, markers map[string]string) {
  jm.runJob(job, nil, markers)
}

func (jm *JobManager) executeJobRun(job *Job, jobRun *JobRunContext) {
  job.lock.Lock()
  count := job.Count
  delay := job.delayD
  initialDelay := job.initialDelayD
  finishTrigger := job.FinishTrigger
  if job.commandTask != nil && jobRun.jobArgs == nil {
    jobRun.jobArgs = job.commandTask.Args
  }
  job.lock.Unlock()

  log.Printf("job [%s] Run [%d] Started \n", job.Name, jobRun.id)
  events.SendEventJSON(Jobs_JobStarted, job.Name, map[string]interface{}{"job": job, "jobRun": jobRun.id})

  time.Sleep(initialDelay)
  for i := 0; i < count; i++ {
    time.Sleep(delay)
    if job.commandTask != nil {
      jm.runCommandJob(job, jobRun, i+1, i == count-1)
    } else if job.httpTask != nil {
      jm.invokeHttpTarget(job, jobRun, i+1, i == count-1)
    }
    jobRun.lock.RLock()
    running := !jobRun.stopped && !jobRun.finished
    jobRun.lock.RUnlock()
    if !running {
      break
    }
  }
  job.lock.Lock()
  jobRun.lock.Lock()
  close(jobRun.stopChannel)
  close(jobRun.doneChannel)
  msg := fmt.Sprintf("job [%s] Run [%d] Finished", job.Name, jobRun.id)
  log.Println(msg)
  events.SendEvent(Jobs_JobFinished, msg)
  jobRun.lock.Unlock()
  job.lock.Unlock()

  if finishTrigger != "" {
    jm.runJobWithInput(finishTrigger, nil)
  }
}

func (jm *JobManager) initJobRun(job *Job, jobArgs []string, markers map[string]string) *JobRunContext {
  if job == nil || (job.commandTask == nil && job.httpTask == nil) {
    return nil
  }
  jobRun := &JobRunContext{}
  jobRun.jobResults = []*JobResult{}
  jobRun.jobArgs = jobArgs
  jobRun.markers = markers
  jobRun.stopChannel = make(chan bool, 10)
  jobRun.doneChannel = make(chan bool, 10)

  job.lock.Lock()
  job.jobRunCounter++
  jobRun.id = job.jobRunCounter
  job.lock.Unlock()

  jm.lock.Lock()
  if jm.jobRuns[job.Name] == nil {
    jm.jobRuns[job.Name] = map[int]*JobRunContext{}
  }
  jm.jobRuns[job.Name][job.jobRunCounter] = jobRun
  jm.lock.Unlock()

  return jobRun
}

func (jm *JobManager) runJob(job *Job, jobArgs []string, markers map[string]string) {
  jobRun := jm.initJobRun(job, jobArgs, markers)
  if jobRun == nil {
    return
  }
  jm.executeJobRun(job, jobRun)
}

func (jm *JobManager) RunJob(job *Job) {
  jm.runJob(job, nil, nil)
}

func (jm *JobManager) runJobs(jobs []*Job) {
  for _, job := range jobs {
    go jm.RunJob(job)
  }
}

func (jm *JobManager) stopJob(j string) bool {
  jm.lock.Lock()
  defer jm.lock.Unlock()
  job := jm.jobs[j]
  jobRuns := jm.jobRuns[j]
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
    if !done && !jobRun.finished && !jobRun.stopped {
      jobRun.stopChannel <- true
    }
    jobRun.lock.Unlock()
    events.SendEventJSON(Jobs_JobStopped, job.Name, job)
  }
  return true
}

func (jm *JobManager) stopJobs(jobs []string) {
  for _, job := range jobs {
    jm.stopJob(job)
  }
}

func RunJobs(jobs []string, port int) {
  _, jobsToRun := Jobs.getJobsToRun(jobs)
  Jobs.runJobs(jobsToRun)
}

func StopJob(job string, port int) bool {
  return Jobs.stopJob(job)
}

func StopJobs(jobs []string, port int) {
  for _, job := range jobs {
    StopJob(job, port)
  }
}

func addJob(w http.ResponseWriter, r *http.Request) {
  msg := ""
  if job, err := ParseJob(r); err == nil {
    Jobs.AddJob(job)
    msg = fmt.Sprintf("Added Job: %s", util.ToJSON(job))
    events.SendRequestEventJSON(Jobs_JobAdded, job.Name, job, r)
  } else {
    w.WriteHeader(http.StatusBadRequest)
    msg = fmt.Sprintf("Failed to add job with error: %s", err.Error())
  }
  fmt.Fprintln(w, msg)
  util.AddLogMessage(msg, r)
}

func storeJobScriptOrFile(w http.ResponseWriter, r *http.Request) {
  msg := ""
  name := util.GetStringParamValue(r, "name")
  path := util.GetStringParamValue(r, "path")
  content := util.ReadBytes(r.Body)
  script := strings.Contains(r.RequestURI, "script")

  if script || path == "" {
    path, _ = os.Getwd()
  }
  if path, ok := Jobs.StoreJobScriptOrFile(path, name, content, script); ok {
    if script {
      msg = fmt.Sprintf("Stored Job Script [%s] at path [%s]", name, path)
      events.SendRequestEvent(Jobs_JobScriptStored, msg, r)
    } else {
      msg = fmt.Sprintf("Stored File [%s] at path [%s]", name, path)
      events.SendRequestEvent(Jobs_JobFileStored, msg, r)
    }
  } else {
    msg = fmt.Sprintf("Failed to store Job file [%s] at path [%s]", name, path)
    w.WriteHeader(http.StatusBadRequest)
  }
  fmt.Fprintln(w, msg)
  util.AddLogMessage(msg, r)
}

func removeJob(w http.ResponseWriter, r *http.Request) {
  msg := ""
  if jobs, present := util.GetListParam(r, "jobs"); present {
    Jobs.removeJobs(jobs)
    msg = fmt.Sprintf("Jobs Removed: %s", jobs)
    events.SendRequestEventJSON(Jobs_JobsRemoved, util.GetStringParamValue(r, "jobs"), jobs, r)
  } else {
    w.WriteHeader(http.StatusNotAcceptable)
    msg = "No jobs"
  }
  fmt.Fprintln(w, msg)
  util.AddLogMessage(msg, r)
}

func getJobs(w http.ResponseWriter, r *http.Request) {
  util.AddLogMessage("Reporting jobs", r)
  util.WriteJsonPayload(w, Jobs.jobs)
}

func runJobs(w http.ResponseWriter, r *http.Request) {
  msg := ""
  names, _ := util.GetListParam(r, "jobs")
  jobNames, jobsToRun := Jobs.getJobsToRun(names)
  if len(jobsToRun) > 0 {
    Jobs.runJobs(jobsToRun)
    msg = fmt.Sprintf("Jobs %+v started", jobNames)
  } else {
    w.WriteHeader(http.StatusNotAcceptable)
    msg = "No jobs to run"
  }
  fmt.Fprintln(w, msg)
  util.AddLogMessage(msg, r)
}

func stopJobs(w http.ResponseWriter, r *http.Request) {
  jobs, present := util.GetListParam(r, "jobs")
  Jobs.lock.RLock()
  if !present {
    for j := range Jobs.jobs {
      jobs = append(jobs, j)
    }
  }
  Jobs.lock.RUnlock()
  Jobs.stopJobs(jobs)
  msg := fmt.Sprintf("Jobs %+v stopped", jobs)
  fmt.Fprintln(w, msg)
  util.AddLogMessage(msg, r)
}

func clearJobs(w http.ResponseWriter, r *http.Request) {
  Jobs.init()
  msg := "Jobs Cleared"
  events.SendRequestEvent(Jobs_JobsCleared, "", r)
  fmt.Fprintln(w, msg)
  util.AddLogMessage(msg, r)
}

func clearJobResults(w http.ResponseWriter, r *http.Request) {
  msg := "Job Results Cleared"
  events.SendRequestEvent(Jobs_JobResultsCleared, "", r)
  Jobs.lock.Lock()
  Jobs.jobRuns = map[string]map[int]*JobRunContext{}
  Jobs.lock.Unlock()
  fmt.Fprintln(w, msg)
  util.AddLogMessage(msg, r)
}

func getJobResults(w http.ResponseWriter, r *http.Request) {
  msg := ""
  if job, present := util.GetStringParam(r, "job"); present {
    msg = fmt.Sprintf("Results reported for job: %s", job)
    util.WriteJsonPayload(w, Jobs.getJobResults(job))
  } else {
    msg = "Results reported for all jobs"
    util.WriteJsonPayload(w, Jobs.getAllJobsResults())
  }
  util.AddLogMessage(msg, r)
}

func ParseJobFromPayload(payload string) (*Job, error) {
  job := &Job{}
  if err := util.ReadJson(payload, job); err == nil {
    if job.delayD, _ = time.ParseDuration(job.Delay); job.delayD == 0 {
      job.delayD = 10 * time.Millisecond
    }
    if job.initialDelayD, _ = time.ParseDuration(job.InitialDelay); job.initialDelayD == 0 {
      job.initialDelayD = 1 * time.Second
    }
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
      commandTask := CommandJobTask{fillers: map[string]int{}}
      var httpTaskError error
      var cmdTaskError error
      task := util.ToJSON(job.Task)
      if httpTaskError = util.ReadJson(task, &httpTask); httpTaskError == nil {
        if httpTaskError = invocation.ValidateSpec(&httpTask.InvocationSpec); httpTaskError == nil {
          httpTask.CollectResponse = true
          job.httpTask = &httpTask
        }
      }
      if httpTaskError != nil {
        if cmdTaskError = util.ReadJson(task, &commandTask); cmdTaskError == nil {
          if commandTask.Cmd != "" {
            for _, arg := range commandTask.Args {
              fillers := util.GetFillers(arg)
              for _, filler := range fillers {
                commandTask.fillers[filler]++
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
