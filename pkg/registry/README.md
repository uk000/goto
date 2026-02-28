# Registry Package REST APIs

This package provides REST APIs for managing registry and lockers.
See [Registry Overview](Overview.md) doc for an overview and examples of Goto's Registry feature.

### Registry Clone, Dump and Load APIs

#### <a name="registry-clone-dump-and-load-apis"></a>
|METHOD            |URI                                             |Description|
|------------------|------------------------------------------------|-----------|
| POST | /registry/cloneFrom?url={url} | Clone data from another registry instance at the given URL. The current goto instance will download `peers`, `lockers`, `targets`, `jobs`, `tracking headers` and `probes`. The peer pods downloaded from the other registry are not used for any invocation by this registry, it just becomes available locally for informational purposes. Any new pods connecting to this registry using the same peer labels will use the downloaded targets, jobs, etc. |
| GET | /registry/dump | Dump current registry configs and locker data in json format. |
| POST | /registry/load | Load registry configs and locker data from json dump produced via `/dump` API. |


### Peer Management APIs

#### <a name="registry-peer-mgmt-apis"></a>
|METHOD            |URI                                             |Description|
|------------------|------------------------------------------------|-----------|
| POST      | /registry/peers/add     | Register a worker instance (referred to as peer). See [Peer JSON Schema](#peer-json-schema)|
| POST      | /registry/peers<br/>/`{peer}`/remember | Re-register a peer. Accepts the same request payload as /peers/add API but doesn't respond back with targets and jobs. |
| POST, PUT | /registry/peers/`{peer}`<br/>/remove/`{address}` | Deregister a peer by its label and IP address |
| GET       | /registry/peers/`{peer}`<br/>/health/`{address}` | Check and report health of a specific peer instance based on label and IP address |
| GET       | /registry/peers<br/>/`{peer}`/health | Check and report health of all instances of a peer |
| GET       | /registry/peers/health | Check and report health of all instances of all peers |
| POST      | /registry/peers/`{peer}`<br/>/health/cleanup | Check health of all instances of the given peer label and remove IP addresses that are unresponsive |
| POST      | /registry/peers<br/>/health/cleanup | Check health of all instances of all peers and remove IP addresses that are unresponsive |
| POST      | /registry/peers<br/>/clear/epochs   | Remove epochs for disconnected peers|
| POST      | /registry/peers/clear   | Remove all registered peers|
| POST      | /registry/peers<br/>/copyToLocker   | Copy current set of `Peers JSON` data (output of `/registry/peers` API) to current locker under a pre-defined key named `peers` |
| GET       | /registry/peers         | Get all registered peers. See [Peers JSON Schema](#peers-json-schema) |


### Peer Targets APIs

These APIs manage client invocation targets on peers, allowing to add, remove, start and stop specific or all targets, and read client invocation results in a processed JSON format.

#### <a name="registry-peer-targets-apis"></a>
|METHOD            |URI                                             |Description|
|------------------|------------------------------------------------|-----------|
| GET       | /registry/peers/targets | Get all registered targets for all peers |
| POST      | /registry/peers<br/>/`{peer}`/targets/add | Add a target to be sent to a peer. See [Peer Target JSON Schema](#peer-target-json-schema). Pushed immediately as well as upon start of a new peer instance. |
| POST, PUT | /registry/peers/`{peer}`<br/>/targets/`{targets}`/remove | Remove given targets for a peer |
| POST      | /registry/peers<br/>/`{peer}`/targets/clear   | Remove all targets for a peer|
| GET       | /registry/peers/`{peer}`/targets   | Get all targets of a peer |
| POST, PUT | /registry/peers/`{peer}`<br/>/targets/`{targets}`/invoke | Invoke given targets on the given peer |
| POST, PUT | /registry/peers/`{peer}`<br/>/targets/invoke/all | Invoke all targets on the given peer |
| POST, PUT | /registry/peers<br/>/targets/invoke/all | Invoke all targets on the given peer |
| POST, PUT | /registry/peers/`{peer}`<br/>/targets/`{targets}`/stop | Stop given targets on the given peer |
| POST, PUT | /registry/peers/`{peer}`<br/>/targets/stop/all | Stop all targets on the given peer |
| POST, PUT | /registry/peers<br/>/targets/stop/all | Stop all targets on the given peer |
| POST      | /registry/peers/targets/clear   | Remove all targets from all peers |
| POST, PUT | /registry/peers<br/>/client/results<br/>/all/`{enable}`  | Controls whether results should be summarized across all targets. Disabling this when not needed can improve performance. Disabled by default. |
| POST, PUT | /registry/peers<br/>/client/results<br/>/invocations/`{enable}`  | Controls whether results should be captured for individual invocations. Disabling this when not needed can reduce memory usage. Disabled by default. |

### Peer Jobs APIs

#### <a name="registry-peer-jobs-apis"></a>
|METHOD            |URI                                             |Description|
|------------------|------------------------------------------------|-----------|
| GET       | /registry/peers/jobs | Get all registered jobs for all peers |
| POST, PUT | /registry/peers/`{peer}`<br/>/jobs/add | Add a job to be sent to a peer. See [Peer Job JSON Schema](#peer-job-json-schema). Pushed immediately as well as upon start of a new peer instance. |
| POST, PUT | /registry/peers/jobs/add | Add a job to be sent to all peers. See [Peer Job JSON Schema](#peer-job-json-schema). Pushed immediately as well as upon start of a new peer instance. |
| POST, PUT | /registry/peers/`{peer}`<br/>/jobs/add/script/`{name}` | Add a job script with the given name and request body as content, to be sent to instances of a peer. Pushed immediately as well as upon start of a new peer instance. |
| POST, PUT | /registry/peers<br/>/jobs/add/script/`{name}` | Add a job script with the given name and request body as content, to be sent to all peers. Pushed immediately as well as upon start of a new peer instance. |
| POST, PUT | /registry/peers/`{peer}`<br/>/jobs/store/file/`{name}` | Add a file with the given name and request body as content, to be sent to instances of a peer, to be saved at the current working directory of the peer `goto` process. Pushed immediately as well as upon start of a new peer instance. |
| POST, PUT | /registry/peers<br/>/jobs/store/file/`{name}` | Add a file with the given name and request body as content, to be sent to all peers, to be saved at the current working directory of the peer `goto` process. Pushed immediately as well as upon start of a new peer instance. |
| POST, PUT | /registry/peers/`{peer}`<br/>/jobs/store/file/`{name}`?path=`{path}` | Add a file with the given name and request body as content, to be sent to instances of a peer, to be saved at the given path in the peer `goto` process' host. Pushed immediately as well as upon start of a new peer instance. |
| POST, PUT | /registry/peers<br/>/jobs/store/file/`{name}`?path=`{path}` | Add a file with the given name and request body as content, to be sent to all peers, to be saved at the given path in the peer `goto` process' host. Pushed immediately as well as upon start of a new peer instance. |
| POST | /registry/peers/`{peer}`<br/>/jobs/`{jobs}`/remove | Remove given jobs for a peer. |
| POST | /registry/peers<br/>/jobs/`{jobs}`/remove | Remove given jobs from all peers. |
| POST      | /registry/peers/`{peer}`<br/>/jobs/clear   | Remove all jobs for a peer.|
| POST      | /registry/peers<br/>/jobs/clear   | Remove all jobs from all peers.|
| GET       | /registry/peers/`{peer}`/jobs   | Get all jobs of a peer |
| GET       | /registry/peers/jobs   | Get all jobs of all peers |
| POST | /registry/peers/`{peer}`<br/>/jobs/`{jobs}`/run | Run given jobs on the given peer |
| POST | /registry/peers<br/>/jobs/`{jobs}`/run | Run given jobs on all peers |
| POST| /registry/peers/`{peer}`<br/>/jobs/run/all | Run all jobs on the given peer |
| POST| /registry/peers<br/>/jobs/run/all | Run all jobs on all peers |
| POST | /registry/peers/`{peer}`<br/>/jobs/`{jobs}`/stop | Stop given jobs on the given peer |
| POST | /registry/peers<br/>/jobs/`{jobs}`/stop | Stop given jobs on all peers |
| POST | /registry/peers/`{peer}`<br/>/jobs/stop/all | Stop all jobs on the given peer |
| POST | /registry/peers<br/>/jobs/stop/all | Stop all jobs on all peers |

### Peer Tracking APIs

#### <a name="registry-peer-tracking-apis"></a>
|METHOD            |URI                                             |Description|
|------------------|------------------------------------------------|-----------|
| POST/PUT         | /registry/peers/track/headers/clear            | Clear tracking headers |
| POST/PUT         | /registry/peers/track/time/clear               | Clear tracking time buckets |
| POST, PUT | /registry/peers<br/>/track/headers/`{headers}` | Configure headers to be tracked by client invocations on peers. Pushed immediately as well as upon start of a new peer instance. |
| GET | /registry/peers/track/headers | Get a list of headers configured for tracking by the above `POST` API. |
| POST, PUT | /registry/peers<br/>/track/time/`{buckets}` | Configure time buckets to be tracked by client invocations on peers. Pushed immediately as well as upon start of a new peer instance. Buckets are added as a comma-separated list of low-high values in millis, e.g. `0-100,101-300,301-1000` |
| GET | /registry/peers/track/time | Get a list of time buckets configured for tracking by the above `POST` API. |


### Peer Probes APIs

#### <a name="registry-peer-probes-apis"></a>
|METHOD            |URI                                             |Description|
|------------------|------------------------------------------------|-----------|
| POST, PUT | /registry/peers/probes<br/>/liveness/set?uri=`{uri}` | Configure liveness probe URI for peers. Pushed immediately as well as upon start of a new peer instance. |
| POST, PUT | /registry/peers/probes<br/>/readiness/set/status=`{status}` | Configure readiness probe status for peers. Pushed immediately as well as upon start of a new peer instance. |
| POST, PUT | /registry/peers/probes<br/>/liveness/set/status=`{status}` | Configure readiness probe status for peers. Pushed immediately as well as upon start of a new peer instance. |
| GET | /registry/peers/probes | Get probe configuration given to registry via any of the above 4 probe APIs. |
| POST, PUT | /registry/peers/probes<br/>/readiness/set?uri=`{uri}` | Configure readiness probe URI for peers. Pushed immediately as well as upon start of a new peer instance. |

### Peer Remote API Invocation APIs

#### <a name="registry-peer-call-apis"></a>
|METHOD            |URI                                             |Description|
|------------------|------------------------------------------------|-----------|
 GET, POST, PUT | /registry/peers/`{peer}`<br/>/call?uri=`{uri}` | Invoke the given `URI` on the given `peer`, using the HTTP method and payload from this request |
| GET, POST, PUT | /registry/peers/call?uri=`{uri}` | Invoke the given `URI` on all `peers`, using the HTTP method and payload from this request |
| GET | /registry/peers/`{peer}`/apis | Get a list of useful APIs ready for invocation on or related to the given peer |
| GET | /registry/peers/apis | Get a list of useful APIs ready for invocation on or related to all peers |



## Locker Management APIs

#### Context Lockers APIs

#### <a name="context-lockers-apis"></a>
|METHOD            |URI                                             |Description|
|------------------|------------------------------------------------|-----------|
| POST             | /registry/context/{context}/lockers/{label}/open | Open a locker in a context |
| POST             | /registry/context/{context}/lockers/{label}/close | Close a locker in a context |
| POST             | /registry/context/{context}/lockers/{label}/clear | Clear a locker in a context |
| POST             | /registry/context/{context}/lockers/{label}/store/{path} | Store data in a locker in a context |
| GET              | /registry/context/{context}/lockers/{label}/get/{path} | Get data from a locker in a context |


#### Lockers Maintenance APIs

#### <a name="lockers-maintenance-apis"></a>
|METHOD            |URI                                             |Description|
|------------------|------------------------------------------------|-----------|
| POST      | /registry/lockers<br/>/open/`{label}` | Setup a locker with the given label and make it the current locker where peer results get stored. Comma-separated labels can be used to open nested lockers, where each non-leaf item in the CSV list is used as a parent locker. The leaf locker label becomes the currently active locker. |
| POST      | /registry/lockers<br/>/close/`{label}` | Remove the locker for the given label. |
| POST      | /registry/lockers<br/>/`{label}`/close | Remove the locker for the given label. |
| POST      | /registry/lockers/close | Remove all labeled lockers and empty the default locker.  |
| POST      | /registry/lockers<br/>/`{label}`/clear | Clear the contents of the locker for the given label but keep the locker. |
| POST      | /registry/lockers/clear | Remove all labeled lockers and empty the default locker.  |
| POST             | /registry/lockers/{label}/open                 | Open a locker |
| POST             | /registry/lockers/{label}/close                | Close a locker |
| POST             | /registry/lockers/{label}/clear                | Clear a locker |
| POST             | /registry/lockers/close                        | Close all lockers |
| POST             | /registry/lockers/clear                        | Clear all lockers |
| POST      | /registry/peers<br/>/`{peer}`/`{address}`<br/>/lockers/clear | Clear the locker for the peer instance under the currently active labeled locker |
| POST      | /registry/peers/`{peer}`<br/>/lockers/clear | Clear the locker for all instances of the given peer under the currently active labeled locker |
| POST      | /registry/peers<br/>/lockers/clear | Clear all peer lockers under the currently active labeled locker |


#### Lockers Data Store APIs

#### <a name="locker-data-store-apis"></a>
|METHOD            |URI                                             |Description|
|------------------|------------------------------------------------|-----------|
| POST      | /registry/lockers<br/>/`{label}`/store/`{path}` | Store payload (body) as data in the given labeled locker at the leaf of the given key path. `path` can be a single key or a comma-separated list of subkeys, in which case data gets stored in the tree under the given path. |
| POST      | /registry/lockers<br/>/`{label}`/remove/`{path}` | Remove stored data, if any, from the given key path in the given labeled locker. `path` can be a single key or a comma-separated list of subkeys, in which case data gets removed from the leaf of the given path. |
| POST      | /registry/peers<br/>/`{peer}`/`{address}`<br/>/locker/store/`{path}` | Store any arbitrary value for the given `path` in the locker of the peer instance under the currently active labeled locker. `path` can be a single key or a comma-separated list of subkeys, in which case data is read from the leaf of the given path. |
| POST      | /registry/peers<br/>/`{peer}`/`{address}`<br/>/locker/remove/`{path}` | Remove stored data for the given `path` from the locker of the peer instance under the currently active labeled locker. `path` can be a comma-separated list of subkeys, in which case the leaf key in the path gets removed. |
| POST      | /registry/peers/`{peer}`<br/>/locker/store/`{path}` | Store any arbitrary value for the given key in the peer locker without associating data to a peer instance under the currently active labeled locker. `path` can be a comma-separated list of subkeys, in which case data gets stored in the tree under the given complete path. |
| POST      | /registry/peers/`{peer}`<br/>/locker/remove/`{path}` | Remove stored data for the given key from the peer locker under the currently active labeled locker. `path` can be a comma-separated list of subkeys, in which case the leaf key in the path gets removed. |


#### Lockers Data Read APIs

These APIs read either all contents of one or more lockers, or read data stored at specific paths/keys. Where applicable, query param `data` controls whether locker is returned with or without stored data (default value is `n` and only locker metadata is fetched). Query param `events` controls whether the locker is returned with or without peers' events data. Query param `peers` controls whether the returned locker(s) should include peer sub-lockers (containing data published by various peers). Query param `level` controls how many levels of subkeys are returned (default level is 2). Label `current` can be used with APIs that take a locker label param to get data from the currently active locker. Comma-separated labels can be used to read from a nested locker.

#### <a name="locker-data-read-apis"></a>
|METHOD            |URI                                             |Description|
|------------------|------------------------------------------------|-----------|
| GET              | /registry/lockers/{label}/get/{path}?data={data}&level={level} | Get data from a locker |
| GET       | /registry/lockers/labels | Get a list of all existing locker labels, regardless of whether or not it has data.  |
| GET              | /registry/lockers?data={data}&events={events}&peers={peers}&level={level} | Get all lockers data with events and peers |
| GET      | /registry/lockers<br/>/`{label}`/data/keys| Get a list of keys where some data is stored, from the given locker. |
| GET      | /registry/lockers<br/>/`{label}`/data/paths| Get a list of key paths (URIs) where some data is stored, from the given locker. The returned URIs are valid for invocation against the base URL of the registry. |
| GET      | /registry/lockers<br/>/data/keys| Get a list of keys where some data is stored, from all lockers.  |
| GET      | /registry/lockers<br/>/data/paths| Get a list of key paths (URIs) where some data is stored, from all lockers. The returned URIs are valid for invocation against the base URL of the registry. |
| GET      | /registry/lockers<br/>/search/`{text}` | Get a list of all valid URI paths (containing the locker label and keys) where the given text exists, across all lockers. The returned URIs are valid for invocation against the base URL of the registry. |
| GET      | /registry/lockers<br/>/`{label}`/search/`{text}` | Get a list of all valid URI paths where the given text exists in the given locker. The returned URIs are valid for invocation against the base URL of the registry. |
| GET       | /registry/lockers<br/>/data?data=`[y/n]`<br/>&level=`{level}` | Get data sub-lockers from all labeled lockers |
| GET       | /registry/lockers/`{label}`<br/>/data?data=`[y/n]`<br/>&level=`{level}` | Get data sub-lockers from the given labeled locker.  |
| GET      | /registry/lockers<br/>/`{label}`/get/`{path}`?<br/>data=`[y/n]`&level=`{level}` | Read stored data, if any, at the given key path in the given labeled locker. `path` can be a single key or a comma-separated list of subkeys, in which case data is read from the leaf of the given path. |


#### Lockers Peers Data Read APIs

These APIs read either all contents of one or more lockers, or read data stored at specific paths/keys. Where applicable, query param `data` controls whether locker is returned with or without stored data (default value is `n` and only locker metadata is fetched). Query param `events` controls whether the locker is returned with or without peers' events data. Query param `peers` controls whether the returned locker(s) should include peer sub-lockers (containing data published by various peers). Query param `level` controls how many levels of subkeys are returned (default level is 2). Label `current` can be used with APIs that take a locker label param to get data from the currently active locker. Comma-separated labels can be used to read from a nested locker.

#### <a name="locker-peers-data-read-apis"></a>
|METHOD            |URI                                             |Description|
|------------------|------------------------------------------------|-----------|
| GET       | /registry/lockers/`{label}`<br/>/peers/`{peer}`/`{address}`<br/>/get/`{path}` | Get the data stored at the given path under the peer instance's locker under the given labeled locker.  |
| GET       | /registry/lockers/`{label}`<br/>/peers/`{peer}`/get/`{path}` | Get the data stored at the given path under the peer locker under the given labeled locker.  |
| GET       | /registry/peers<br/>/`{peer}`/`{address}`<br/>/locker/get/`{path}` | Get the data stored at the given path under the peer instance's locker under the current labeled locker |
| GET       | /registry/peers/`{peer}`<br/>/locker/get/`{path}` | Get the data stored at the given path under the peer locker under the current labeled locker |
| GET       | /registry/lockers/`{label}`<br/>/peers/`{peer}`/client<br/>/results`[/targets={targets}]` | Get detailed invocation results for the given peer (results of all instances grouped together) from the given labeled locker, optionally filtered for the given targets (comma-separated list).  |
| GET       | /registry/lockers/`{label}`<br/>/peers/`{peer}`/instances<br/>/client<br/>/results`[/targets={targets}]` | Get detailed invocation results for the given peer's instances (reported per instance) from the given labeled locker, optionally filtered for the given targets (comma-separated list).  |
| GET       | /registry/lockers/`{label}`<br/>/peers/`{peer}`/client<br/>/results/summary<br/>`[/targets={targets}]` | Get invocation summary results for the given peer from the given labeled locker, optionally filtered for the given targets (comma-separated list).  |
| GET       | /registry/lockers/`{label}`<br/>/peers/`{peer}`<br/>/client/results<br/>`[/targets={targets}]` | Get invocation summary results for the given peer from the given labeled locker, optionally filtered for the given targets (comma-separated list).  |
| GET       | /registry/lockers/`{label}`<br/>/peers/client<br/>/results/details<br/>`[/targets={targets}]` | Get detailed invocation results for all peers (results of all instances grouped together) from the given labeled locker, optionally filtered for the given targets (comma-separated list).  |
| GET       | /registry/lockers/`{label}`<br/>/peers/instances<br/>/client/results<br/>`[/targets={targets}]` | Get detailed invocation results for all peer instances (reported per instance) from the given labeled locker, optionally filtered for the given targets (comma-separated list).  |
| GET       | /registry/lockers/`{label}`<br/>/peers/client<br/>/results/summary<br/>`[/targets={targets}]` | Get invocation summary results for all peers from the given labeled locker, optionally filtered for the given targets (comma-separated list).  |
| GET       | /registry/lockers/`{label}`<br/>/peers/client/results<br/>`[/targets={targets}]` | Get invocation summary results for all peers from the given labeled locker, optionally filtered for the given targets (comma-separated list).  |
| GET       | /registry/peers/`{peer}`<br/>/client/results/details<br/>`[/targets={targets}]` | Get detailed invocation results for the given peer (results of all instances grouped together) from the current locker, optionally filtered for the given targets (comma-separated list).  |
| GET       | /registry/peers/`{peer}`<br/>/instances/client/results<br/>`[/targets={targets}]` | Get detailed invocation results for the given peer's instances (reported per instance) from the current locker, optionally filtered for the given targets (comma-separated list).  |
| GET       | /registry/peers/`{peer}`<br/>/client/results/summary<br/>`[/targets={targets}]` | Get invocation summary results for the given peer from the current locker, optionally filtered for the given targets (comma-separated list).  |
| GET       | /registry/peers/`{peer}`<br/>/client/results<br/>`[/targets={targets}]` | Get invocation summary results for the given peer from the current locker, optionally filtered for the given targets (comma-separated list).  |
| GET       | /registry/peers/client<br/>/results/details<br/>`[/targets={targets}]` | Get detailed invocation results for all peers (results of all instances grouped together) from the current locker, optionally filtered for the given targets (comma-separated list).  |
| GET       | /registry/peers<br/>/instances/client/results<br/>`[/targets={targets}]` | Get detailed invocation results for all peer instances (reported per instance) from the current locker, optionally filtered for the given targets (comma-separated list).  |
| GET       | /registry/peers/client<br/>/results/summary<br/>`[/targets={targets}]` | Get invocation summary results for all peers from the current locker, optionally filtered for the given targets (comma-separated list).  |
| GET       | /registry/peers<br/>/client/results<br/>`[/targets={targets}]` | Get invocation summary results for all peers from the current locker, optionally filtered for the given targets (comma-separated list).  |
| GET       | /registry/peers<br/>/`{peer}`/`{address}`/lockers?<br/>data=`[y/n]`&events=`[y/n]`<br/>&level=`{level}` | Get the peer instance's locker from the current active labeled locker. |
| GET       | /registry/peers/`{peer}`<br/>/lockers?data=`[y/n]`<br/>&events=`[y/n]`&level=`{level}` | Get locker's data for all instances of the peer from the currently active labeled locker |
| GET       | /registry/peers<br/>/lockers?data=`[y/n]`<br/>&events=`[y/n]`&level=`{level}` | Get the lockers of all peers from the currently active labeled locker. |
| GET       | /registry/lockers/`{label}`<br/>/peers/`{peer}`/`{address}`?<br/>data=`[y/n]`&events=`[y/n]`<br/>&level=`{level}` | Get the peer instance's locker from the given labeled locker. |
| GET       | /registry/lockers/`{label}`<br/>/peers/`{peer}`?data=`[y/n]`<br/>&events=`[y/n]`&level=`{level}` | Get the lockers of all instances of the given peer from the given labeled locker. |
| GET       | /registry/lockers/`{label}`<br/>/peers?data=`[y/n]`<br/>&events=`[y/n]`&level=`{level}` | Get the lockers of all peers from the given labeled locker. |
| GET       | /registry/lockers/`{label}`?<br/>data=`[y/n]`&events=`[y/n]`<br/>&peers=`[y/n]`&level=`{level}` | Get the given labeled locker.  |
| GET       | /registry/lockers?<br/>data=`[y/n]`&events=`[y/n]`<br/>&peers=`[y/n]`&level=`{level}` | Get all lockers. |


#### Lockers Events APIs

Label `current` and `all` can be used with these APIs to get data from the currently active locker or all lockers. Param `unified=y` produces a single timeline of events combined from various peers. Param `reverse=y` produces the timeline in reverse chronological order. By default events are returned with their `data` field set to `...` to reduce the amount of data returned. Param `data=y` returns the events with data. 

#### <a name="locker-events-apis"></a>
|METHOD            |URI                                             |Description|
|------------------|------------------------------------------------|-----------|
| POST      | /registry/peers<br/>/`{peer}`/`{address}`<br/>/events/store | API invoked by peers to publish their events to the currently active locker. Event timeline can be retrieved from the registry via various `/events` GET APIs. |
| POST      | /registry/peers<br/>/events/flush | Requests all peer instances to publish any pending events to registry, and clears events timeline on the peer instances. Registry still retains the peers' events in the current locker. |
| POST      | /registry/peers<br/>/events/clear | Requests all peer instances to clear their events timeline, and also removes the peers events from the current registry locker. |
| GET       | /registry/lockers<br/>/`{label}`/peers<br/>/`{peers}`/events?<br/>reverse=`[y/n]`<br/>&data=`[y/n]` | Get the events timeline for all instances of the given peers (comma-separated list) from the given labeled locker. |
| GET       | /registry/lockers/`{label}`<br/>/peers/events?<br/>unified=`[y/n]`<br/>&reverse=`[y/n]`<br/>&data=`[y/n]` | Get the events timeline for all instances of all peers from the given labeled locker, grouped by peer label. |
| GET       | /registry/peers<br/>/`{peer}`/events?<br/>reverse=`[y/n]`<br/>&data=`[y/n]` | Get the events timeline for all instances of the given peer from the current locker. |
| GET       | /registry/peers/events?<br/>unified=`[y/n]`<br/>&reverse=`[y/n]`<br/>&data=`[y/n]` | Get the events timeline for all instances of all peers from the current locker, grouped by peer label. |
| GET       | /registry/lockers/`{label}`<br/>/peers/`{peers}`<br/>/events/search/`{text}`?<br/>reverse=`[y/n]`<br/>&data=`[y/n]` | Search in the events timeline for all instances of the given peers (comma-separated list) from the given labeled locker. |
| GET       | /registry/lockers/`{label}`<br/>/peers/events<br/>/search/`{text}`?<br/>unified=`[y/n]`<br/>&reverse=`[y/n]`<br/>&data=`[y/n]` | Search in the events timeline of all peers from the given labeled locker, grouped by peer label. |
| GET       | /registry/peers/`{peer}`<br/>/events/search/`{text}`?<br/>reverse=`[y/n]`&<br/>data=`[y/n]` | Search in the events timeline for all instances of the given peer from the current locker. |
| GET       | /registry/peers/events<br/>/search/`{text}`?<br/>unified=`[y/n]`<br/>&reverse=`[y/n]`<br/>&data=`[y/n]` | Search in the events timeline of all peers from the current locker, grouped by peer label. |


<details>
<summary> Registry Timeline Events </summary>

- `Registry: Peer Added`
- `Registry: Peer Rejected`
- `Registry: Peer Removed`
- `Registry: Checked Peers Health`
- `Registry: Cleaned Up Unhealthy Peers`
- `Registry: Locker Opened`
- `Registry: Locker Closed`
- `Registry: Locker Cleared`
- `Registry: All Lockers Cleared`
- `Registry: Locker Data Stored`
- `Registry: Locker Data Removed`
- `Registry: Peer Instance Locker Cleared`
- `Registry: Peer Locker Cleared`
- `Registry: All Peer Lockers Cleared`
- `Registry: Peer Events Cleared`
- `Registry: Peer Results Cleared`
- `Registry: Peer Target Rejected`
- `Registry: Peer Target Added`
- `Registry: Peer Targets Removed`
- `Registry: Peer Targets Stopped`
- `Registry: Peer Targets Invoked`
- `Registry: Peer Job Rejected`
- `Registry: Peer Job Added`
- `Registry: Peer Job File Added`
- `Registry: Peer Job Rejected`
- `Registry: Peer Job Script Rejected`
- `Registry: Peer Jobs Removed`
- `Registry: Peer Jobs Stopped`
- `Registry: Peer Jobs Invoked`
- `Registry: Peers Epochs Cleared`
- `Registry: Peers Cleared`
- `Registry: Peer Targets Cleared`
- `Registry: Peer Jobs Cleared`
- `Registry: Peer Tracking Headers Added`
- `Registry: Peer Probe Set`
- `Registry: Peer Probe Status Set`
- `Registry: Peer Called`
- `Registry: Peers Copied`
- `Registry: Lockers Dumped`
- `Registry: Dumped`
- `Registry: Dump Loaded`
- `Registry: Cloned`

#### Peer Timeline Events for registry interactions
- `Peer Registered`
- `Peer Startup Data`
- `Peer Deregistered`

</details>

See [Registry Schema JSONs and APIs Examples](../../docs/registry-api-examples.md)
