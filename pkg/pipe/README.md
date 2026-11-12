# Pipeline Feature

`Goto` pipelines feature allows you to pull data from various kinds of sources, process the source data through one or more transformations, and feed the output back to more sources/transformations or write it out. Pipelines support various kinds of sources (K8s, Jobs, Command Scripts, HTTP traffic, etc.) and transformations (JSONPath, JQ, Go Template, Regex). Additionally, pipelines support `watch` capability, where sources are watched for new data and the associated pipeline is triggered for any upstream changes.

By default all sources and transformation of a pipeline are executed in a single stage. Pipeline stages can be defined to achieve a more complex orchestration where some sources and transformations need to execute before others.


### Pipeline Spec
A pipeline is defined via a JSON payload submitted via API. 

|FIELD|TYPE|Description|
|---|---|---|
| name | string | Name of the pipeline |
| sources | map[string]Source | Set of sources that feed into this pipeline, either all at once or in stages (see stages below). The map key is the source name. |
| transforms | map[string]Transform | Set of transformations that get applied to data produced by sources at various stages. The map key is the transformation name. |
| stages | []PipelineStage | Optional list of stages this pipeline is split into. Each stage consists of a set of sources and transforms. When no stages are given, a single stage labeled `default` is created that holds all the sources and then all the transforms, each in order of their names. See [Pipeline Stage Spec](#pipeline-stage-spec) below. |
| outs | []string | Names of sources and transformations whose output is included in the final output of the pipeline. When not specified, the pipeline output includes the output of all its sources and transformations. (Note: the field is named `outs`; a field named `out` is ignored.)  |
| running | bool | A read-only status field to indicate whether the pipeline is currently executing. Any value posted for it is ignored. |

#### <a name="pipeline-stage-spec"></a>Pipeline Stage Spec

|FIELD|TYPE|Description|
|---|---|---|
| label | string | Name of the stage, used in logs |
| sources | []string | Names of the sources (keys of the pipeline's `sources`) to be fetched in this stage, in the given order. A name that doesn't exist is skipped. |
| transforms | []string | Names of the transforms (keys of the pipeline's `transforms`) to be applied after the sources of this stage were fetched, in the given order. |
| delay | duration string | Optional delay (e.g. `500ms`) before the stage starts. Shown as `"0s"` when not set. |

A pipeline run executes the stages in order over a shared workspace: each source stores its output in the workspace under the source's name, and each transform stores its result under its own name, so later stages can read (and use as `{name}` fillers in `spec`, `content` or `input`) everything produced by earlier stages. At the end, the entries named in `outs` (or the whole workspace if `outs` is not given) are the output of the run, which is recorded under the next run ID of the pipeline and returned to the caller (a run started by a trigger writes its output as YAML to the stdout of the `goto` process). A pipeline runs one run at a time: a manual run waits for a run that is on; a trigger that arrives while the pipeline is running is dropped.

<br/>
<details>
<summary> Pipeline Spec Example </summary>

```
{
  "name": "demo-pipe",
  "sources": {
    "source_ns": {"type":"K8s", "spec":"/v1/ns/goto", "watch": true},
    "source_job": {"type":"Job", "spec":"job1"}
  },
  "transforms": {
    "ns_name": {"type": "JQ", "spec": ".source_ns.metadata.name"},
    "result": {"type": "Template", "spec": "{{.source_job}}"}
  },
  "stages": [
    {"label": "stage1", "sources":["source_ns"], "transforms":["ns_name"]},
    {"label": "stage2", "sources":["source_job"], "transforms":["result"]}
  ],
  "outs": ["ns_name", "result"]
}
```

</details>
<br/>


## Pipeline Sources
A pipeline source brings data into the pipeline, and can also trigger the pipeline when watched. 

### Pipeline Source Spec

|FIELD|TYPE|Description|
|---|---|---|
| name | string | Name of the source. It is always set to the key of the source in the pipeline's `sources` map (or the `{name}` of the add-source APIs). |
| type | string | Identifies the type of the source: `K8s`, `K8sPodExec`, `Job`, `Script`, `HTTPRequest` or `Tunnel` (case-sensitive). A source of any other type is accepted but produces nothing. |
| spec | string | Provides identifying information for the source. A source's spec may use fillers with syntax `{name}` where `name` identifies another source or transformation whose output should be used to substitute the filler. See examples further below. |
| content | string | Used for sources that need some content for execution, e.g. a script. It can use fillers to capture output of other sources/transformations similar to `spec` field. |
| input | any | Optional input to be given to the source at execution. It can use fillers to capture output of other sources/transformations similar to `spec` field. |
| inputSource | string | Optional name of another source whose output should be passed as input to this source |
| parseJSON | bool | Whether the output of this source be parsed as JSON. If the output is not valid JSON it is kept as text. Takes precedence over `parseNumber`. |
| parseNumber | bool | Whether the output of this source be parsed as an integer number (the output must be a whole number, else it is kept as text) |
| reuseIfExists | bool | Applies to `Job` sources: whether to use the results of the latest run of the job if it has any, instead of running the job again |
| watch | bool | Whether the source should be watched for new input data and trigger the pipeline. This flag is honored by `K8s` sources only. `Job`, `HTTPRequest` and `Tunnel` sources always trigger the pipeline, whatever this flag says: a `Job` source when a run of its job finishes (registered once the pipeline has run at least once), and an `HTTPRequest`/`Tunnel` source when a matching request arrives (registered as soon as the pipeline is added). |

Notes on source output:
- The output of a source is stored in the pipeline workspace under the source's name. A list output with a single element is stored as that element. A source that produces no output (e.g. an unknown K8s resource, a failed pod exec) is simply absent from the workspace.
- A source's `spec`, `content` and `input` may contain `{name}` fillers that are replaced with the workspace entry of that name (a source or transform of an earlier stage). `inputSource` names a workspace entry whose value is set as the source's input before it runs.


### Pipeline Source Types
The following kinds of sources are available:

#### <i>Source Type: `Job`</i>
<div style="padding-left:20px">
  A Job source represents the output of a goto [Job](../job/README.md#jobs-features). The source `spec` field refers to an existing Job's name, where the job must be previously defined using [Jobs APIs](../job/README.md#jobs-apis). 
  <br/><br/>
  Job sources should mostly be defined with `reuseIfExists` set to `true`, indicating that the pipeline should use the output of the last run of the linked job. This is even more relevant when the source is configured with `watch` set to true, so that an execution of the job would trigger the pipeline and the pipeline would simply use the output of the job run that triggered it. If `reuseIfExists` field is set to false (or the job has no results yet), the pipeline's execution will trigger a fresh execution of the linked job (the source's `input`, if any, is passed to the job as its raw input, i.e. as the request payload of an HTTP job), and the pipeline would wait up to 10 seconds for the job to complete and use the result produced by this job run. The source output is the list of the results of the job run: the output lines for a command job (including the final empty result), or the invocation result as JSON text for an HTTP job.
  <br/><br/>
  As `goto` supports two types of jobs: `Command` and `HTTP`, hence a pipeline can use Job sources to execute OS scripts as well as make HTTP calls.
  <br/><br/>
  See [Jobs feature](../job/README.md#jobs-features) for details on how to define Command and HTTP jobs. Note that adding a pipeline that has a Job source removes all the job watchers that were registered for that job (including by other pipelines).

  <details>
  <summary> Job Source Example </summary>

  ```
  {
    "name": "demo-jobs-pipe",
    "sources": {
      "s_cmd_job": {"type":"Job", "spec":"job1", "reuseIfExists": true, "watch": true},
      "s_http_job": {"type":"Job", "spec":"job2", "parseJSON": true, "reuseIfExists": true, "watch": true}
    }
  }
  ```
  </details>

</div>

#### <i>Source Type: `Script`</i>
<div style="padding-left:20px">

  A Script source provides a way to run an OS script in a pipeline without a predefined job. The source `spec` field provides the script name, the `content` field provides the script, and the `input` or `inputSource` field can be used to provide input for the script if needed (it is given to the script as its standard input). The content is registered as an in-memory [script](../scripts/README.md) under the `spec` name (so it also shows up in `GET /scripts`), and is run as `sh -c <content>`. The source output is the text written by the script to stdout/stderr, including the trailing newline. 

  <details>
  <summary> Script Source Example </summary>

  ```
  {
    "name": "demo-script-pipe",
    "sources": {
      "s1": {
        "type":"Script", 
        "spec":"echo-foo", 
        "content":"echo 'FooX\\nBarX\\nFoo2\\nAnother Foo\\nMore Foos\\nDone'"
      },		
      "s2": {
        "type":"Script", 
        "spec":"foo-array", 
        "content":"grep Foo | sed 's/X/!/g' | tr -s ' ' | jq -R -s -c 'gsub(\"^\\\\s+|\\\\s+$\";\"\") | split(\"\\n\")' ", 
        "inputSource": "s1",
        "parseJSON": true
      },
      "s3": {
        "type":"Script", 
        "spec":"count-lines", 
        "content":"wc -l | xargs echo -n Total Lines: ", 
        "inputSource": "s1"
      },
      "s4": {
        "type":"Script", 
        "spec":"hello-world", 
        "content":"sed 's/Foo/World/g' | xargs echo -n Hello ", 
        "input": "{t1}"
      },
      "s5": {
        "type":"Script", 
        "spec":"foo-length", 
        "content":"jq -R -s -c 'length' | xargs echo -n Char Count: ", 
        "input": "{s2}",
        "parseNumber": true
      }
    },
    "transforms": {
      "t1": {"type": "JQ", "spec": ".s2[0]"},
      "t2": {"type": "JQ", "spec": ".s2[2]"},
      "t3": {"type": "JQ", "spec": ".s2 | length"}
    },
    "stages": [
      {"label": "stage1", "sources":["s1"]},
      {"label": "stage2", "sources":["s2", "s3"], "transforms":["t1", "t2", "t3"]},
      {"label": "stage3", "sources":["s4", "s5"]}
    ],
    "outs": ["s3", "s4", "s5"]
  }
  ```
  </details>
</div>

#### <i>Source Type: `K8s`</i>
<div style="padding-left:20px">

  A K8s source represents either a single K8s resource or a set of K8s resources, identified by its `spec` field. It queries a K8s cluster to fetch the resource details: either from the local cluster where `goto` instance is deployed, or a remote cluster based on the current kube context set in local kube config.

  The K8s source `spec` identifies the K8s resource using pattern `group/version/kind/namespace/name`. For example, spec value `networking.istio.io/v1beta1/virtualservice/foo/bar` identifies a resource named `bar` under namespace `foo` with resource kind `VirtualService`, group `networking.istio.io`, and version `v1beta1`. 

  For native k8s resources that don't have a group, the group piece is left empty. For example: `/v1/ns/` indicates all namespaces, `/v1/foo/pods` identifies all pods in namespace `foo`, and `/v1//pods` indicates all pods across all namespaces. 

  > See [K8s feature](../k8s/README.md#k8s-features) for more details about K8s query support in `goto`. The source output is the resource (or the list of resources) as JSON. With `watch: true`, an add/update/delete of the resource triggers the pipeline.

  <details>
  <summary> K8S Source Example </summary>

  ```
  {
    "name": "demo-k8s-pipe",
    "sources": {
      "ns": {"type":"K8s", "spec":"/v1/ns/goto", "watch": true},
      "nspods": {"type":"K8s", "spec":"/v1/pod/{ns_name}"}
    },
    "transforms": {
      "ns_name": {"type": "JQ", "spec": ".ns.metadata.name"},
      "podnames": {"type": "JQ", "spec": ".nspods.items[]|{name: .metadata.name, containers:[.spec.containers[].name]}"}
    },
    "stages": [
      {"label": "stage1", "sources":["ns"], "transforms":["ns_name"]},
      {"label": "stage2", "sources":["nspods"], "transforms":["podnames"]}
    ],
    "outs": ["ns_name", "podnames"]
  }
  ```
  </details>
</div>

#### <i>Source Type: `K8sPodExec`</i>
<div style="padding-left:20px">

  This source type allows executing a command on one or more K8s pods. The source `spec` field should be defined in the format `"namespace/pod-label-selector/container-name"` (a `spec` that doesn't have exactly these 3 parts produces no output), and the spec `content` field should contain the command(s) to be executed on the selected pods. If the source has an `input`, the command that gets executed is `echo '<input>' | xargs <content>`. The source output is the output of the command.

  <details>
  <summary> K8S Pod Exec Source Example </summary>
  ```
  {
    "name": "demo-podexec-pipe",
    "sources": {
      "pod_source": {"type":"K8sPodExec", "spec":"gotons/app=goto/goto", "content":"ls /"}
    }
  }
  ```
  </details>

</div>


#### <i>Source Type: `HTTPRequest`</i>
<div style="padding-left:20px">

  This source type allows for pipelines to be triggered based on HTTP requests received by the `goto` server. The feature is achieved via two sets of configurations: 
  1. A [Trigger](../server/response/trigger/README.md#response-triggers) that defines the request/response match criteria (URI, Headers, Status Code) that should match in order for the request to trigger a pipeline.
  2. A pipeline that includes an `HTTPRequest` source that references the trigger name in its `spec` field.

  When the `goto` server receives an HTTP request matching the trigger criteria, the linked pipeline gets triggered and the pipeline source's output carries the HTTP response data along with some metadata as listed below:
  1. `request.trigger`: name of the trigger that matched the request
  2. `request.host`
  3. `request.uri`
  4. `request.headers`
  5. `request.body`
  6. `response.status` (the status code as a number)
  7. `response.headers`

  Until the first matching request arrives, `request` and `response` are `null`. Only the last matching request is kept.

  <details>
  <summary> HTTP Source Example </summary>

  ```
  #HTTP Trigger definition
  {
    "name": "t1",
    "pipe": true,
    "enabled": true,
    "triggerURIs": ["/foo", "/status/*"],
    "triggerStatuses": [502]
  }

  #Trigger based pipeline definition
  {
    "name": "demo-http-trigger-pipe",
    "sources": {
      "http": {"type":"HTTPRequest", "spec":"t1", "watch": true}
    },
    "transforms": {
      "uri": {"type": "JQ", "spec": ".http.request.uri"},
      "req_headers": {"type": "JQ", "spec": ".http.request.headers"},
      "status": {"type": "JQ", "spec": ".http.response.status"},
      "resp_headers": {"type": "JQ", "spec": ".http.response.headers"}
    }
  }

  #This curl call to the goto instance triggers the pipeline via the trigger that matches on URI + response status code.
  $ curl http://goto:8080/status/502
  ```

  </details>
</div>


#### <i>Source Type: `Tunnel`</i>
<div style="padding-left:20px">

  This source type allows triggering pipelines for HTTP requests tunneled through a `goto` instance. The output behavior of `Tunnel` source type is somewhat similar to the `HTTPRequest` source type, but they differ in which HTTP requests would trigger the pipeline. The `HTTPRequest` source type triggers pipelines for requests served by the `goto` instance itself as a server, whereas the `Tunnel` source type comes into play for HTTP requests meant for other upstream destinations but tunneled via a `goto` instance for inspection.

  The tunnel associated with the pipeline is referenced in the source `spec` field by the tunnel's `Endpoint` identifier that's composed as `<protocol>:<address>:<port>`. See [Tunnel](../tunnel/README.md#tunnel) feature for more details about tunnel creation and handling.

  The source output has the same shape as for `HTTPRequest`, with `request` carrying `host`, `uri`, `headers` and `body`, and `response` carrying `endpoint`, `status`, `headers` and `body` (the response body of the tunneled call).

  <details>
  <summary> Tunnel Source Example </summary>

  For an HTTP request tunneled via goto instance `goto-1.goto` to the final destination `goto-2.goto`, a pipeline on `goto-1` instance can use `Tunnel` source that references `goto-2` endpoint in its spec as shown below. The pipeline will be triggered for all requests that pass through `goto-1` with `goto-2` as the final destination.

  ```
  {
    "name": "demo-tunnel-pipe",
    "sources": {
      "http": {"type":"Tunnel", "spec":"http:goto-2.goto:9091", "watch": true}
    },
    "transforms": {
      "uri": {"type": "JQ", "spec": ".http.request.uri"},
      "req_headers": {"type": "JQ", "spec": ".http.request.headers"},
      "status": {"type": "JQ", "spec": ".http.response.status"},
      "resp_headers": {"type": "JQ", "spec": ".http.response.headers"}
    }
  }
  ```

  </details>
</div>

### Pipeline Transformations

Pipeline's transformation steps provide you a way to extract a subset of information from a source's output and/or apply some basic computational logic to the source data to produce some derived information.

A transformation definition provides the implementation-specific transformation query in its `spec` field. Each transformation receives the current working context as input, and so the query can refer to any existing source or transformation by name that's expected to exist in the working context at the time of execution of that transformation. Starting with any existing source or transformation, the query can read data from the source's output.

### Pipeline Transformation Spec

|FIELD|TYPE|Description|
|---|---|---|
| name | string | Name of the transformation, provided as the map key in the pipeline JSON (the key is always used as the name) |
| type | string | Identifies the type of the transformation. Supported types are: `JSONPath`, `JQ`, `Template` and `Regex` (case-sensitive). A transform of any other type is accepted but does nothing. |
| spec | string | Provides the query code to be compiled and executed based on the transformation type. For `JSONPath`, the braces `{...}` around the expression are optional. An invalid `Template` or `Regex` spec is not rejected: the transform then produces an empty result.

Each transformation is applied to the whole workspace (a JSON object holding the outputs of all the sources and transformations that ran so far, keyed by their names), and its result is stored in the workspace under the transformation's name: `JSONPath` and `JQ` store the query result, `Template` stores the rendered text, and `Regex` stores the list of all matches found in the text form of the workspace.

<br/>
The following kinds of transformations are supported:

1. `JSONPath`: based on implementation `k8s.io/client-go/util/jsonpath`
2. `JQ`: based on implementation `github.com/itchyny/gojq`
3. `Template`: based on golang templates feature
4. `Regex`: based on golang regexp package

# <a name="pipe-apis"></a>
###  Pipeline APIs

|METHOD|URI|Description|
|---|---|---|
| POST, PUT | /pipes/create/`{name}` | Create an empty pipeline that will be filled via other APIs (replaces any existing pipeline of that name). Responds `200` with `Pipe [name] created`. The pipeline has no stages, see the note on sources below. |
| POST, PUT | /pipes/add | Add a pipeline using JSON payload (see [Pipeline Spec](#pipeline-spec)), replacing any existing pipeline of the same name. Responds `200` with `Pipe [name] added`, or also `200` with `Failed to parse pipe with error: ...` if the payload isn't valid JSON. |
| POST, PUT | /pipes/`{name}`/clear,<br/>/pipes/clear/`{name}` | Empty the given pipeline: removes all its sources (its transforms, stages and recorded runs are kept). Responds `200` with `Pipe [name] cleared`, also if the pipeline doesn't exist. |
| POST, PUT | /pipes/remove/`{name}`,<br/>/pipes/`{name}`/remove | Remove the given pipeline. Responds `200` with `Pipe [name] removed`, also if it doesn't exist. Its recorded runs are kept. |
| POST, PUT | /pipes/`{pipe}`/sources/add | Add a source via JSON payload (see [Pipeline Source Spec](#pipeline-source-spec); `name` is required) to the given existing pipeline. Responds `200` with `Added Source [name] with Resource Spec [spec] to Pipe [pipe]`, or `200` with `Failed to add source with error: ...` (e.g. `Pipe [x] doesn't exist.`). Note that the source is not added to any stage, and a pipeline made via `/pipes/create/{name}` has no stages, so only sources that are listed in the stages of a pipeline added via `/pipes/add` get executed (a run of a pipeline without stages produces `{}`). |
| POST, PUT | /pipes/`{pipe}`/sources<br/>/remove/`{name}` | Remove the given source from the given pipeline. Responds `200` with `Removed Source [name] from Pipe [pipe]`, or `200` with `Failed to remove source ...` if the pipeline doesn't exist. |
| POST, PUT | /pipes/`{pipe}`/sources<br/>/add/k8s/`{name}`?<br/>`spec={spec}` | Add a K8s source with the given `name` and `spec` to the given existing pipeline. The `spec` query param is required for the route to match (without it the response is `404`); an empty `spec=` gets `400` with `Missing pipe and/or source name`. |
| POST, PUT | /pipes/`{pipe}`/sources<br/>/add/script/`{name}` | Add a script source with the given `name` and content (body payload) to the given pipeline. The script is registered under the source name (spec = name). Responds `200` with `Added Script Source [name] to Pipe [pipe]` (also when the pipeline doesn't exist, in which case nothing is added). |
| POST | /pipes/clear | Remove all defined pipelines and their recorded runs. Responds `200` with `All pipes cleared`. |
| POST | /pipes/`{name}`/run | Run the given pipeline manually (as opposed to pipelines getting triggered by sources). Responds with the output of the run as JSON (YAML with request header `Accept: application/yaml`). `500` with `Failed to run pipeline [name] with error: Pipe [name] doesn't exist.` for an unknown pipeline. |
| GET | /pipes | Get details of currently defined pipelines, as a JSON object keyed by pipeline name |
| GET | /pipes/`{name}` | Get details including run logs of the given pipeline, as `{"pipe": {pipeline}, "runs": [{"id": 1, "name": "pipe", "out": {...}}]}` (`pipe` and `runs` are `null` for an unknown pipeline). Always JSON: the YAML `Accept` header gives an empty response. |


## Notes
- `{pipe}` or `{name}` - Pipeline name
- `spec` query parameter - K8s resource specification for K8s sources
- Pipeline specifications should be provided as JSON in the request body
- The run IDs are counted across all pipelines, and all runs are retained (until the pipeline APIs clear them) with their full output.

#### Example
```
# define a pipeline with two stages (output of the first source is the input of the second)
curl -X POST localhost:8080/pipes/add --data '
{
  "name": "pp",
  "sources": {
    "s1": {"type":"Script", "spec":"echo-foo", "content":"echo FooX; echo BarX"},
    "s2": {"type":"Script", "spec":"cnt", "content":"wc -l | xargs echo -n Lines:", "inputSource":"s1"}
  },
  "transforms": {
    "t1": {"type":"Regex", "spec":"[A-Z][a-z]+"},
    "t2": {"type":"Template", "spec":"[{{.s2}}]"}
  },
  "stages": [
    {"label":"st1", "sources":["s1"]},
    {"label":"st2", "sources":["s2"], "transforms":["t1","t2"], "delay":"100ms"}
  ],
  "outs": ["t1","t2"]
}'
# Pipe [pp] added

curl -X POST localhost:8080/pipes/pp/run
# {"t1": ["Foo", "Bar", "Lines"], "t2": "[Lines: 2]"}

curl -X POST -H 'Accept: application/yaml' localhost:8080/pipes/pp/run
curl localhost:8080/pipes/pp
curl -X POST localhost:8080/pipes/pp/clear
curl -X POST localhost:8080/pipes/pp/remove
```
