
# <a name="jobs-features"></a>
# Jobs Features

`Goto` allows jobs to be configured that can be run manually or auto-start upon addition. Two kinds of jobs are supported:
- HTTP requests to be made to some target URL
- Command execution on local OS.
The job results can be retrieved via API from the `goto` instance, and also stored in lockers on the Goto registry instance if enabled. (See `--locker` command arg)

Jobs can be configured to run periodically using the `cron` field. A cron job starts automatically upon creation, and keeps running at the specified frequency until stopped (using `/jobs/stop` API). A stopped cron job can be restarted using `/jobs/run` API, which restarts the cron frequency.

Jobs can also trigger another job for each line of output produced, as well as upon completion. For command jobs, the output produced is split by newline, and each line of output can be used as input to trigger another command job. A job can specify markers for output fields (split using specified separator), and these markers can be referenced by successor jobs. The markers from a job's output are carried over to all its successor jobs, so a job can use output from a parent job that might be several generations in the past. The triggered job's command arg specifies marker references as `{foo}`, which gets replaced by the value extracted from any predecessor job's output with that marker key. This feature can be used to trigger complex chains of jobs, where each job uses output of the previous job to do something else.

###### <small> [Back to TOC](#jobs) </small>


#### Jobs APIs
|METHOD|URI|Description|
|---|---|---|
| POST, PUT  |	/jobs/add     | Add a job. See [Job JSON Schema](#job-json-schema) |
| POST, PUT  |	/jobs/update | Update a job, using [Job JSON Schema](#job-json-schema) |
| POST, PUT  |	/jobs/add<br/>/script/`{name}` | Add a shell script to be executed as a job, by storing the request body as script content under the given filename at the current working directory of the `goto` process. Also creates a default job with the same name to provide a ready-to-use way to execute the script. |
| POST, PUT  |	/jobs/store<br/>/file/`{name}` | Store request body as a file at the current working directory of the `goto` process. Filed saved with mode `777`.|
| POST, PUT  |	/jobs/store/file<br/>/`{name}`?path=`{path}` | Store request body as a file at the given path with mode `777`. |
| POST  | /jobs/`{jobs}`/remove | Remove given jobs by name, and clears its results |
| POST  | /jobs/clear         | Remove all jobs |
| POST  | /jobs/`{jobs}`/run `[or]` /jobs/run/`{jobs}` | Run given jobs |
| POST  | /jobs/run/all       | Run all configured jobs |
| POST  | /jobs/`{jobs}`/stop | Stop given jobs if running |
| POST  | /jobs/stop/all      | Stop all running jobs |
| GET   | /jobs/{job}/results | Get results for the given job's runs |
| GET   | /jobs/results       | Get results for all jobs |
| POST   | /jobs/results/clear | Clear all job results |
| GET   | /jobs/scripts       | Get a list of all stored scripts |
| GET   | /jobs/              | Get a list of all configured jobs |


<br/>

<details>
<summary> Job JSON Schema </summary>

#### 
|Field|Data Type|Description|
|---|---|---|
| name          | string        | Identifies this job |
| task          | JSON          | Task to be executed for this job. Can be an [HTTP Task](#job-http-task-json-schema) or [Command Task](#job-command-task-json-schema) |
| auto          | bool          | Whether the job should be started automatically as soon as it's posted. |
| delay         | duration      | Minimum delay at start of each iteration of the job. Actual effective delay may be higher than this. |
| initialDelay  | duration      | Minimum delay to wait before starting a job. Actual effective delay may be higher than this. |
| count         | int           | Number of times this job should be executed during a single invocation |
| cron          | string        | This field allows configuring the job to be executed periodically. The frequency can be specified in cron format (`* * * * *`) or as a duration (e.g. `15s`).|
| maxResults    | int           | Number of max results to be received from the job, after which the job is stopped |
| keepResults   | int           | Number of results to be retained from an invocation of the job |
| keepFirst     | bool          | Indicates whether the first invocation result should be retained, reducing the slots for capturing remaining results by (maxResults-1) |
| timeout       | duration      | Duration after which the job is forcefully stopped if not finished |
| outputTrigger | JobTrigger    | ID of another job to trigger for each output produced by this job. For command jobs, words from this job's output can be injected into the command of the next job using positional references (described above) |
| finishTrigger | JobTrigger        | ID of another job to trigger upon completion of this job |

#### Job HTTP Task JSON Schema
|Field|Data Type|Description|
|---|---|---|
| {Invocation Spec} | Target Invocation Spec | See [Client Target JSON Schema](../../docs/client-api-json-schemas.md) that's shared by the HTTP Jobs to define an HTTP target invocation |
| parseJSON    | bool           | Indicates whether the response payload is expected to be JSON and hence not to treat it as text (to avoid escaping quotes in JSON) |
| transforms   | []Transform | A set of transformations to be applied to the JSON output of the job. See [Response Payload Transformation](#-payload-transformation) section for details of JSON transformation supported by `goto`. |

#### Job Command Task JSON Schema
|Field|Data Type|Description|
|---|---|---|
| cmd             | string         | Command to be executed on the OS. Use `sh` as command if shell features are to be used (e.g. pipe) |
| script          | string         | Name of a stored script. When a script is uploaded using API `/jobs/add/script/{name}`, a script job gets created automatically with the uploaded script name stored in this field. |
| args            | []string       | Arguments to be passed to the OS command |
| outputMarkers   | map[int]string | Specifies marker keys to use to reference the output fields from each line of output. Output is split using the specified separator to extract its keys. Positioning starts at 1 for first the piece of split output. |
| outputSeparator | string         | Text to be used as separator to split each line of output of this command to extract its fields, which are then used by markers |


#### Job Trigger JSON Schema
|Field|Data Type|Description|
|---|---|---|
| name     | string     | Name of the target job to trigger from the current job.  |
| forwardPayload  | bool | Whether to forward the output of the current job as the input payload for the next job. Currently this is only supported for HTTP task jobs. |


#### Job Result JSON Schema
|Field|Data Type|Description|
|---|---|---|
| id     | string     | id uniquely identifies a result item within a job run, using format `<JobRunCounter>.<JobIteration>.<ResultCount>`.  |
| finished  | bool       | whether the job run has finished at the time of producing this result |
| stopped   | bool       | whether the job was stopped at the time of producing this result |
| last      | bool       | whether this result is an output of the last iteration of this job run |
| time      | time       | time when this result was produced |
| data      | string     | Result data |

</details>

<details>
<summary> Jobs Timeline Events </summary>

- `Job Added`
- `Job Script Stored`
- `Job File Stored`
- `Jobs Removed`
- `Jobs Cleared`
- `Job Results Cleared`
- `Job Started`
- `Job Finished`
- `Job Stopped`

</details>

See [Jobs Example](../../docs/jobs-example.md)
