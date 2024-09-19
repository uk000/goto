/**
 * Copyright 2024 uk
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

package job

import (
  "bufio"
  "fmt"
  "goto/pkg/util"
  "log"
  "os/exec"
  "strings"
  "sync"
  "time"
)

func (jm *JobManager) runCommandWithInput(job *Job, markers map[string]string, rawInput []byte) *JobRunContext {
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
  return jm.runJob(job, args, markers, rawInput)
}

func (jm *JobManager) runCommandJob(job *Job, jobRun *JobRunContext, iteration int, last bool) {
  job.lock.RLock()
  jobRun.lock.RLock()
  commandTask := job.commandTask
  targetCommand := commandTask.Cmd
  var args []string
  script := jm.scripts[commandTask.Script]
  if targetCommand == "" && script != nil {
    if script.Shell {
      targetCommand = "sh"
      args = []string{script.Path}
      args = append(args, jobRun.jobArgs...)
    } else {
      targetCommand = script.Path
      args = jobRun.jobArgs
    }
  } else {
    args = jobRun.jobArgs
  }
  realCmd := targetCommand + " " + strings.Join(args, " ")
  log.Printf("Jobs: Job [%s] Running command [%s]\n", job.Name, realCmd)
  cmd := exec.Command(targetCommand, args...)
  outputTrigger := job.OutputTrigger
  maxResults := job.MaxResults
  stopChannel := jobRun.stopChannel
  doneChannel := jobRun.doneChannel
  jobRun.lock.RUnlock()
  job.lock.RUnlock()
  stdout, err1 := cmd.StdoutPipe()
  stderr, err2 := cmd.StderrPipe()
  if err1 != nil || err2 != nil {
    log.Printf("Jobs: Job [%s] failed to open output stream from command: %s\n", job.Name, realCmd)
    return
  }
  outScanner := bufio.NewScanner(stdout)
  errScanner := bufio.NewScanner(stderr)

  if err := cmd.Start(); err != nil {
    msg := fmt.Sprintf("Jobs: Job [%s] failed to execute command [%s] with error [%s]", job.Name, realCmd, err.Error())
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
      log.Printf("Jobs: Job [%s] failed to stop command [%s] with error [%s]\n", job.Name, realCmd, err.Error())
    }
  }

  processOutput := func(data string, stopAfterMax bool) {
    if maxResults == 0 || resultCount < maxResults {
      if data != "" {
        resultCount++
        storeJobResult(job, jobRun, iteration, data, last)
        if outputTrigger != nil {
          if markers := prepareCommandMarkers(data, commandTask, jobRun); len(markers) > 0 {
            go jm.runJobWithInput(outputTrigger.Name, markers, nil)
          }
        }
      }
    } else if stopAfterMax {
      stopCommand()
    }
  }

  timeout := job.Timeout
  if timeout <= 0 {
    timeout = 24 * time.Hour
  }
Done:
  for {
    select {
    case <-time.After(timeout):
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
      processOutput(out, true)
    }
  }
  cmd.Wait()
  for out := range outputChannel {
    processOutput(out, false)
  }
  if last {
    jobRun.lock.Lock()
    jobRun.finished = true
    jobRun.lock.Unlock()
    storeJobResult(job, jobRun, iteration, "", last)
  }
  jobRun.outDoneChannel <- true
}

func prepareCommandMarkers(output string, sourceCommand *CommandJobTask, jobRun *JobRunContext) map[string]string {
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
        markers[util.MarkFiller(outputMarkers[i+1])] = piece
      }
    }
  }
  return markers
}
