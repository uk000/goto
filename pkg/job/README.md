
# <a name="jobs-features"></a>
# Jobs Features

`Goto` allows jobs to be configured that can be run manually or auto-start upon addition. Two kinds of jobs are supported:
- HTTP requests to be made to some target URL
- Command execution on local OS.
The job results can be retrieved via API from the `goto` instance, and also stored in lockers on the Goto registry instance if enabled. (See `--locker` command arg)

Jobs can be configured to run periodically using the `cron` field. A cron job starts automatically upon creation, and keeps running at the specified frequency until stopped (using `/jobs/stop` API). A stopped cron job can be restarted using `/jobs/run` API, which restarts the cron frequency.

Jobs can also trigger another job for each line of output produced, as well as upon completion. For command jobs, the output produced is split by newline, and each line of output can be used as input to trigger another command job. A job can specify markers for output fields (split using specified separator), and these markers can be referenced by successor jobs. The markers from a job's output are carried over to all its successor jobs, so a job can use output from a parent job that might be several generations in the past. The triggered job's command arg specifies marker references as `{foo}`, which gets replaced by the value extracted from any predecessor job's output with that marker key. This feature can be used to trigger complex chains of jobs, where each job uses output of the previous job to do something else. (Note: a command job triggers its `outputTrigger` job only if it defines `outputMarkers`.)

Jobs are also pushed to `goto` peers by the registry, see [Registry Peer Jobs APIs](../registry/README.md#registry-peer-jobs-apis). The results of a job can feed a [Pipeline](../pipe/README.md) as a job source. Jobs are defined via the APIs below or pushed by the registry: a `jobs` section in a goto config file (`--config`, `goto ctl apply`) is currently not applied.

###### <small> [Back to TOC](#jobs-features) </small>


#### <a name="jobs-apis"></a>Jobs APIs
|METHOD|URI|Description|
|---|---|---|
| POST, PUT  |	/jobs/add     | Add a job. See [Job JSON Schema](#job-json-schema). Responds `200` with `Added Job: {job json}`, or `Updated Job: {job json}` if a job with the same name existed (the existing job, its cron schedule and its run results are replaced). `400` with `Failed to add job with error: ...` if the payload isn't valid JSON or the task is missing/invalid. A job with `auto: true` or a `cron` starts right away. |
| POST, PUT  |	/jobs/update | Same as `/jobs/add`. |
| POST, PUT  |	/jobs/add<br/>/script/`{name}` | Add a shell script to be executed as a job, by storing the request body as script content under the given filename at the current working directory of the `goto` process (the name can carry an extension, e.g. `hello.sh`). The script is registered under the file name without its extension (`hello`), and a ready-to-use job with that name is created: `{"name": "hello", "task": {"script": "hello"}, "count": 1, "keepResults": 3}`. A script whose file has the `.sh` extension is run as `sh <path> <args>`; any other script file is executed directly (the file is saved with mode `777`, so it needs a shebang line). Responds `200` with `Stored New Job Script [hello] at path [<absolute path>], and Job created with name [hello]`, or `400` with `Failed to store Job file [...] at path [...]`. |
| POST, PUT  |	/jobs/store<br/>/file/`{name}` | Store request body as a file at the current working directory of the `goto` process. File saved with mode `777`. The file is registered as a script named after the file name without extension (listed by `/jobs/scripts`), but no job is created. Responds `200` with `Stored New File [name] at path [path]`. |
| POST, PUT  |	/jobs/store/file<br/>/`{name}`?path=`{path}` | Store request body as a file at the given path (directories are created as needed) with mode `777`. Note: if the request URI (including the query string) contains the text `script`, the request is handled as a script upload (the `path` is ignored). |
| POST  | /jobs/`{jobs}`/remove | Remove given jobs (comma-separated names) and their results, and stop their cron schedule. Names that don't exist are ignored. Responds `200` with `Jobs Removed: [names]`. A run that is in progress is not stopped, so stop the job first if needed. |
| POST  | /jobs/clear         | Remove all jobs, runs/results, stored script registrations and cron schedules, and all job watchers (used by pipelines). Responds `200` with `Jobs Cleared`. Script and other files that were stored on disk are not deleted. |
| POST  | /jobs/`{jobs}`/run `[or]` /jobs/run/`{jobs}` | Run given jobs (comma-separated names) in the background. Responds `200` with `Jobs [names] started`, or `406` with `No jobs to run` if none of the names exists. Each run gets the next run number of the job. For a job that has a `cron`, this (re)starts its schedule instead of running immediately: the first run happens after one cron interval (plus the job's `initialDelay`). |
| POST  | /jobs/run/all       | Run all configured jobs. Responds `406` with `No jobs to run` if there are no jobs. |
| POST  | /jobs/`{jobs}`/stop | Stop given jobs: removes the cron schedule of the job and signals its unfinished runs to stop (a command is killed, an HTTP call is aborted). Responds `200` with `Jobs [names] stopped`, even if a name doesn't exist. |
| POST  | /jobs/stop/all      | Stop all jobs. |
| GET   | /jobs/{job}/results | Get results for the given job's runs, as `{"<runNumber>": [JobResult...]}`. `{}` if the job has no runs or doesn't exist. See [Job Result JSON Schema](#job-result-json-schema). |
| GET   | /jobs/results       | Get results for all jobs, as `{"<job>": {"<runNumber>": [JobResult...]}}`. |
| POST   | /jobs/results/clear | Clear all job results (the run numbers of a job keep increasing). Responds `200` with `Job Results Cleared`. |
| GET   | /jobs/scripts       | Get all stored scripts and files, as `{"<name>": {"name": "hello", "path": "/abs/path/hello.sh", "shell": true}}`. `shell` is true for `.sh` files. |
| GET   | /jobs              | Get all configured jobs as `{"<name>": Job}`. The `task` of each job is shown the way it was submitted. |

<br/>

<details>
<summary> Job JSON Schema </summary>

#### <a name="job-json-schema"></a>
|Field|Data Type|Default|Description|
|---|---|---|---|
| name          | string        || Identifies this job. Required (an empty name is accepted by the API but the job can't be addressed). Adding a job with an existing name replaces that job. |
| task          | JSON          || Task to be executed for this job (required). Can be an [HTTP Task](#job-http-task-json-schema) or [Command Task](#job-command-task-json-schema). The task is first tried as an HTTP task (it must then have `name` and `url`), else as a command task (it must have `cmd` or `script`); if neither fits, the API responds `400` with both errors. |
| auto          | bool          |false| Whether the job should be started automatically as soon as it's posted. Always true if `cron` is set. |
| delay         | duration string |`10ms`| Minimum delay at start of each iteration of the job (e.g. `200ms`, `1s`). Actual effective delay may be higher than this. |
| initialDelay  | duration string |`1s`| Delay to wait at the start of each run before the first iteration. |
| count         | int           |1| Number of times this job should be executed (iterations) during a single run of the job |
| cron          | string        || This field allows configuring the job to be executed periodically. The frequency can be specified in cron format (`* * * * *`, evaluated in UTC) or as a duration (e.g. `15s`). Each tick starts a new run. An invalid cron spec is accepted by the API but the job is not scheduled (an error is logged). |
| maxResults    | int           |20| Maximum number of results to be received per iteration of the job (output lines of a command, or call results of an HTTP target), after which the iteration is stopped. |
| keepResults   | int           |5| Number of results to be retained per run of the job. When more are produced, the oldest result is dropped. |
| keepFirst     | bool          |false| Indicates whether the first result of a run should be retained, so that when the limit is reached the second result is dropped instead of the first. |
| timeout       | int (nanoseconds) |`600000000000` (10m)| Time after which an iteration is forcefully stopped if not finished. Note that this is a number of nanoseconds (e.g. `30000000000` for 30s), not a duration string. |
| outputTrigger | JobTrigger    || Another job to trigger for each output (result) produced by this job. For command jobs, this works only if the job defines `outputMarkers`, and words from the output are injected into the args of the next job using marker references (described above). |
| finishTrigger | JobTrigger    || Another job to trigger upon completion of each run of this job |

Notes:
- A job created via a script upload gets only `count: 1` and `keepResults: 3`: the defaults above for `delay`, `initialDelay`, `maxResults` and `timeout` are not applied to it (no delays, no result limit, 24h timeout).
- The `outputTrigger`/`finishTrigger` job must exist when the trigger fires, else it is ignored. Triggers are not guarded against loops: a job that triggers itself keeps running.
- `finishTrigger` with `forwardPayload` passes the response payloads of the kept HTTP results of the run as one Go-formatted list, e.g. `[payload1 payload2]`, as the request payload of the next HTTP job.

#### <a name="job-http-task-json-schema"></a>Job HTTP Task JSON Schema
|Field|Data Type|Description|
|---|---|---|
| {Invocation Spec} | Target Invocation Spec | See [Client Target JSON Schema](../../docs/client-api-json-schemas.md) that's shared by the HTTP Jobs to define an HTTP target invocation. `name` and `url` are required. `headers` is a JSON object of name to value. The task's `name` should be unique, as it identifies the call results of this job. `replicas`/`requestCount` produce multiple results per iteration. The job forces `collectResponse` and `trackPayload` to true. |
| parseJSON    | bool           | Accepted but currently has no effect. |
| transforms   | []Transform | A set of transformations to be applied to the payload that is passed on to the job triggered by `forwardPayload` (see Job Trigger below). See [Payload Transformation Schema](../server/response/payload/README.md#payload-transformation-schema) for the transformation definition supported by `goto`. For `outputTrigger`, the transforms of the triggered HTTP job are applied; for `finishTrigger`, the transforms of the finishing HTTP job are applied. |

#### <a name="job-command-task-json-schema"></a>Job Command Task JSON Schema
|Field|Data Type|Description|
|---|---|---|
| cmd             | string         | Command to be executed on the OS (no shell is involved). Use `sh` as command with args `["-c", "..."]` if shell features are to be used (e.g. pipe). |
| script          | string         | Name of a stored script (see `/jobs/scripts`), used if `cmd` is empty. When a script is uploaded using API `/jobs/add/script/{name}`, a script job gets created automatically with the uploaded script name stored in this field. |
| args            | []string       | Arguments to be passed to the OS command or script. May contain `{marker}` references that are filled from the output of the predecessor job that triggered this job. |
| outputMarkers   | map[int]string | Specifies marker keys to use to reference the output fields from each line of output. Output is split using the specified separator to extract its keys. Positioning starts at 1 for first the piece of split output, e.g. `{"1": "date", "3": "msg"}` (JSON object keys are strings). |
| outputSeparator | string         | Text to be used as separator to split each line of output of this command to extract its fields, which are then used by markers. Defaults to a single space. |

Both stdout and stderr of the command are captured line by line, empty lines are skipped, and each line is stored as a result.

#### Job Trigger JSON Schema
|Field|Data Type|Description|
|---|---|---|
| name     | string     | Name of the target job to trigger from the current job.  |
| forwardPayload  | bool | Whether to forward the response payload of the current job as the request payload of the next job. Currently this is only supported for HTTP task jobs (the next job must be an HTTP job). |


#### <a name="job-result-json-schema"></a>Job Result JSON Schema
|Field|Data Type|Description|
|---|---|---|
| id     | string     | id uniquely identifies a result item within a job run, using format `<JobRunCounter>.<JobIteration>.<ResultCount>`.  |
| finished  | bool       | whether the job run has finished at the time of producing this result |
| stopped   | bool       | whether the job was stopped at the time of producing this result |
| last      | bool       | whether this result is an output of the last iteration of this job run |
| time      | time       | time when this result was produced |
| data      | string or object | Result data: the output line (string) for a command job, and the `InvocationResult` object (see [Invocation Result Schema](../../docs/client-api-json-schemas.md)) for an HTTP job. A command job appends a final result with empty `data` and `finished: true` when its last iteration ends. |

</details>

See [Jobs Example](../../docs/jobs-example.md)
