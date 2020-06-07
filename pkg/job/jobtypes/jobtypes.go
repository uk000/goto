package jobtypes

import (
	"errors"
	"fmt"
	"goto/pkg/http/invocation"
	"goto/pkg/util"
	"net/http"
	"sync"
	"time"
)

type CommandJobTask struct {
  Cmd  string   `json:"cmd"`
  Args []string `json."args"`
}

type HttpJobTask struct {
  invocation.InvocationSpec
  ParseJSON bool
}

type JobResult struct {
  Index    string
  Finished bool
  Data     interface{}
}

type JobRunInfo struct {
  Index       int
  Running     bool
  StopChannel chan bool
  DoneChannel chan bool
  Lock        sync.RWMutex
}

type Job struct {
  ID          string          `json:"id"`
  Task        interface{}     `json:"task"`
  Auto        bool            `json:"auto"`
  Delay       string          `json:"delay"`
  Count       int             `json:"count"`
  MaxResults  int             `json:"maxResults"`
  KeepFirst   bool            `json:"keepFirst"`
  DelayD      time.Duration   `json:"-"`
  HttpTask    *HttpJobTask    `json:"-"`
  CommandTask *CommandJobTask `json:"-"`
  JobRun      *JobRunInfo     `json:"-"`
  JobResults  []*JobResult    `json:"-"`
  ResultCount int             `json:"-"`
  Lock        sync.RWMutex    `json:"-"`
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
          job.HttpTask = &httpTask
        }
      }
      if httpTaskError != nil {
        if cmdTaskError = util.ReadJson(task, &commandTask); cmdTaskError == nil {
          if commandTask.Cmd != "" {
            job.CommandTask = &commandTask
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
    return nil, fmt.Errorf("Failed to parse json")
  }
}

func ParseJob(r *http.Request) (*Job, error) {
  return ParseJobFromPayload(util.Read(r.Body))
}
