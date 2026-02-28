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
| stages | []PipelineStage | Optional list of stages this pipeline is split into. Each stage consists of a set of sources and transforms. |
| out | []string | Names of sources and transformations whose output is included in the final output of the pipeline. When not specified, the pipeline output includes the output of all its sources and transformations.  |
| running | bool | A read-only status field to indicate whether the pipeline is currently executing. |

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
  "out": ["ns_name", "result"]
}
```

</details>
<br/>


## Pipeline Sources
A pipeline source brings data into the pipeline, and can also trigger the pipeline when watched. 

### Pipeline Source Spec

|FIELD|TYPE|Description|
|---|---|---|
| name | string | Name of the source |
| type | string | Identifies the type of the source |
| spec | string | Provides identifying information for the source. A source's spec may use fillers with syntax `{name}` where `name` identifies another source or transformation whose output should be used to substitute the filler. See examples further below. |
| content | string | Used for sources that need some content for execution, e.g. a script. It can use fillers to capture output of other sources/transformations similar to `spec` field. |
| input | any | Optional input to be given to the source at execution. It can use fillers to capture output of other sources/transformations similar to `spec` field. |
| inputSource | string | Optional name of another source whose output should be passed as input to this source |
| parseJSON | bool | Whether the output of this source be parsed as JSON |
| parseNumber | bool | Whether the output of this source be parsed as a number |
| reuseIfExists | bool | Whether an existing instance of this source be reused if already instantiated in a previous execution of this pipeline |
| watch | bool | Whether the source should be watched for new input data and trigger the pipeline |


### Pipeline Source Types
The following kinds of sources are available:

#### <i>Source Type: `Job`</i>
<div style="padding-left:20px">
  A Job source represents the output of a goto [Job](#jobs-features). The source `spec` field refers to an existing Job's name, where the job must be previously defined using [Jobs APIs](#jobs-apis). 
  <br/><br/>
  Job sources should mostly be defined with `reuseIfExists` set to `true`, indicating that the pipeline should use the output of the last run of the linked job. This is even more relevant when the source is configured with `watch` set to true, so that an execution of the job would trigger the pipeline and the pipeline would simply use the output of the job run that triggered it. If `reuseIfExists` field is set to false, the pipeline's execution will trigger a fresh execution of the linked job, and the pipeline would wait for the job to complete and use the result produced by this job run.
  <br/><br/>
  As `goto` supports two types of jobs: `Command` and `HTTP`, hence a pipeline can use Job sources to execute OS scripts as well as make HTTP calls.
  <br/><br/>
  See [Jobs feature](#jobs-features) for details on how to define Command and HTTP jobs.

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

  A Script source provides a way to run an OS script in a pipeline without a predefined job. The source `spec` field provides the script name, the `content` field provides the script, and the `input` or `inputSource` field can be used to provide input for the script if needed. 

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
    "out": ["s3", "s4", "s5"]
  }
  ```
  </details>
</div>

#### <i>Source Type: `K8s`</i>
<div style="padding-left:20px">

  A K8s source represents either a single K8s resource or a set of K8s resources, identified by its `spec` field. It queries a K8s cluster to fetch the resource details: either from the local cluster where `goto` instance is deployed, or a remote cluster based on the current kube context set in local kube config.

  The K8s source `spec` identifies the K8s resource using pattern `group/version/kind/namespace/name`. For example, spec value `networking.istio.io/v1beta1/virtualservice/foo/bar` identifies a resource named `bar` under namespace `foo` with resource kind `VirtualService`, group `networking.istio.io`, and version `v1beta1`. 

  For native k8s resources that don't have a group, the group piece is left empty. For example: `/v1/ns/` indicates all namespaces, `/v1/foo/pods` identifies all pods in namespace `foo`, and `/v1//pods` indicates all pods across all namespaces. 

  > See [K8s feature](#k8s-features) for more details about K8s query support in `goto`.

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
    "out": ["ns_name", "podnames"]
  }
  ```
  </details>
</div>

#### <i>Source Type: `K8sPodExec`</i>
<div style="padding-left:20px">

  This source type allows executing a command on one or more K8s pods. The source `spec` field should be defined in the format `"namespace/pod-label-selector/container-name"`, and the spec `content` field should contain the command(s) to be executed on the selected pods.

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
  1. A [Trigger](#response-triggers) that defines the request/response match criteria (URI, Headers, Status Code) that should match in order for the request to trigger a pipeline.
  2. A pipeline that includes an `HTTPRequest` source that references the trigger name in its `spec` field.

  When the `goto` server receives an HTTP request matching the trigger criteria, the linked pipeline gets triggered and the pipeline source's output carries the HTTP response data along with some metadata as listed below:
  1. `request.trigger`: name of the trigger that matched the request
  2. `request.host`
  3. `request.uri`
  4. `request.headers`
  5. `request.body`
  6. `response.status`
  7. `response.headers`

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

  The tunnel associated with the pipeline is referenced in the source `spec` field by the tunnel's `Endpoint` identifier that's composed as `<protocol>:<address>:<port>`. See [Tunnel](#tunnel) feature for more details about tunnel creation and handling.

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
| name | string | Name of the transformation, provided as the map key in the pipeline JSON |
| type | string | Identifies the type of the transformation. Supported types are: `JSONPath`, `JQ`, `Template` and `Regex`. |
| spec | string | Provides the query code to be compiled and executed based on the transformation type.

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
| POST, PUT | /pipes/create/`{name}` | Create an empty pipeline that will be filled via other APIs |
| POST, PUT | /pipes/add | Add a pipeline using JSON payload |
| POST, PUT | /pipes/`{name}`/clear,<br/>/pipes/clear/`{name}` | Empty the given pipeline |
| POST, PUT | /pipes/remove/`{name}`,<br/>/pipes/`{name}`/remove | Remove the given pipeline |
| POST, PUT | /pipes/`{pipe}`/sources/add | Add a source via JSON payload to the given existing pipeline |
| POST, PUT | /pipes/`{pipe}`/sources<br/>/remove/`{name}` | Remove the given source from the given pipeline |
| POST, PUT | /pipes/`{pipe}`/sources<br/>/add/k8s/`{name}`?<br/>`spec={spec}` | Add a K8s source with the given `name` and `spec` to the given existing pipeline |
| POST, PUT | /pipes/`{pipe}`/sources<br/>/add/script/`{name}` | Add a script source with the given `name` and content (body payload) to the given pipeline |
| POST | /pipes/clear | Remove all defined pipelines |
| POST | /pipes/`{name}`/run | Run the given pipeline manually (as opposed to pipelines getting triggered by sources) |
| GET | /pipes | Get details of currently defined pipelines |
| GET | /pipes/`{name}` | Get details including run logs of the given pipeline |


## Notes
- `{pipe}` or `{name}` - Pipeline name
- `spec` query parameter - K8s resource specification for K8s sources
- Pipeline specifications should be provided as JSON in the request body
