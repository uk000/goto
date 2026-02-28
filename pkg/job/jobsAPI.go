/**
 * Copyright 2025 uk
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
	"fmt"
	"goto/pkg/events"
	"goto/pkg/server/middleware"
	"goto/pkg/util"
	"net/http"
	"os"
	"strings"

	"github.com/gorilla/mux"
)

var (
	Middleware = middleware.NewMiddleware("jobs", setRoutes, nil)
)

func setRoutes(r *mux.Router, parent *mux.Router, root *mux.Router) {
	jobsRouter := r.PathPrefix("/jobs").Subrouter()
	util.AddRoute(jobsRouter, "/add", addOrUpdateJob, "POST", "PUT")
	util.AddRoute(jobsRouter, "/update", addOrUpdateJob, "POST", "PUT")
	util.AddRoute(jobsRouter, "/add/script/{name}", storeJobScriptOrFile, "POST", "PUT")
	util.AddRouteQO(jobsRouter, "/store/file/{name}", storeJobScriptOrFile, "path", "POST", "PUT")
	util.AddRoute(jobsRouter, "/{jobs}/remove", removeJob, "POST")
	util.AddRoute(jobsRouter, "/clear", clearJobs, "POST")
	util.AddRoute(jobsRouter, "/run/all", runJobs, "POST")
	util.AddRoute(jobsRouter, "/run/{jobs}", runJobs, "POST")
	util.AddRoute(jobsRouter, "/{jobs}/run", runJobs, "POST")
	util.AddRoute(jobsRouter, "/{jobs}/stop", stopJobs, "POST")
	util.AddRoute(jobsRouter, "/stop/all", stopJobs, "POST")
	util.AddRoute(jobsRouter, "/{job}/results", getJobResults, "GET")
	util.AddRoute(jobsRouter, "/results", getJobResults, "GET")
	util.AddRoute(jobsRouter, "/results/clear", clearJobResults, "POST")
	util.AddRoute(jobsRouter, "/scripts", getScripts, "GET")
	util.AddRoute(jobsRouter, "", getJobs, "GET")
}

func addOrUpdateJob(w http.ResponseWriter, r *http.Request) {
	msg := ""
	if job, err := ParseJob(r); err == nil {
		if existing, err := Manager.AddJob(job); err != nil {
			msg = fmt.Sprintf("Failed to add/update job: %s", util.ToJSONText(job))
		} else if existing {
			msg = fmt.Sprintf("Updated Job: %s", util.ToJSONText(job))
		} else {
			msg = fmt.Sprintf("Added Job: %s", util.ToJSONText(job))
		}
		events.SendRequestEventJSON(events.Jobs_JobAdded, job.Name, job, r)
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
	exists := Manager.scripts[name] != nil
	if scriptName, path, ok := Manager.StoreJobScriptOrFile(path, name, content, script); ok {
		existingOrNew := "Stored New"
		if exists {
			existingOrNew = "Replaced Existing"
		}
		if script {
			msg = fmt.Sprintf("%s Job Script [%s] at path [%s], and Job created with name [%s]", existingOrNew, scriptName, path, scriptName)
			events.SendRequestEvent(events.Jobs_JobScriptStored, msg, r)
		} else {
			msg = fmt.Sprintf("%s File [%s] at path [%s]", existingOrNew, name, path)
			events.SendRequestEvent(events.Jobs_JobFileStored, msg, r)
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
		Manager.removeJobs(jobs)
		msg = fmt.Sprintf("Jobs Removed: %s", jobs)
		events.SendRequestEventJSON(events.Jobs_JobsRemoved, util.GetStringParamValue(r, "jobs"), jobs, r)
	} else {
		w.WriteHeader(http.StatusNotAcceptable)
		msg = "No jobs"
	}
	fmt.Fprintln(w, msg)
	util.AddLogMessage(msg, r)
}

func getJobs(w http.ResponseWriter, r *http.Request) {
	util.AddLogMessage("Reporting jobs", r)
	util.WriteJsonPayload(w, Manager.jobs)
}

func getScripts(w http.ResponseWriter, r *http.Request) {
	util.AddLogMessage("Reporting job scripts ", r)
	util.WriteJsonPayload(w, Manager.scripts)
}

func runJobs(w http.ResponseWriter, r *http.Request) {
	msg := ""
	names, _ := util.GetListParam(r, "jobs")
	jobNames, jobsToRun := Manager.getJobsToRun(names)
	if len(jobsToRun) > 0 {
		Manager.runJobs(jobsToRun)
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
	Manager.lock.RLock()
	if !present {
		for j := range Manager.jobs {
			jobs = append(jobs, j)
		}
	}
	Manager.lock.RUnlock()
	Manager.stopJobs(jobs)
	msg := fmt.Sprintf("Jobs %+v stopped", jobs)
	fmt.Fprintln(w, msg)
	util.AddLogMessage(msg, r)
}

func clearJobs(w http.ResponseWriter, r *http.Request) {
	Manager.init()
	msg := "Jobs Cleared"
	events.SendRequestEvent(events.Jobs_JobsCleared, "", r)
	fmt.Fprintln(w, msg)
	util.AddLogMessage(msg, r)
}

func clearJobResults(w http.ResponseWriter, r *http.Request) {
	msg := "Job Results Cleared"
	events.SendRequestEvent(events.Jobs_JobResultsCleared, "", r)
	Manager.lock.Lock()
	Manager.jobRuns = map[string]map[int]*JobRunContext{}
	Manager.lock.Unlock()
	fmt.Fprintln(w, msg)
	util.AddLogMessage(msg, r)
}

func getJobResults(w http.ResponseWriter, r *http.Request) {
	msg := ""
	if job, present := util.GetStringParam(r, "job"); present {
		msg = fmt.Sprintf("Results reported for job: %s", job)
		util.WriteJsonPayload(w, Manager.getJobResults(job))
	} else {
		msg = "Results reported for all jobs"
		util.WriteJsonPayload(w, Manager.getAllJobsResults())
	}
	util.AddLogMessage(msg, r)
}

func ParseJob(r *http.Request) (*Job, error) {
	return ParseJobFromPayload(util.Read(r.Body))
}
