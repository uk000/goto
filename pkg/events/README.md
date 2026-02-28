
# <a name="events"></a>
## > Events
`goto` logs various events as it performs operations, responds to admin requests and serves traffic. The Events APIs can be used to read and clear events on a `goto` instance. Additionally, if the `goto` instance is configured to report to a registry, it sends the events to the registry. On the Goto registry, events from various peer instances are collected and merged by peer labels. Registry exposes additional APIs to get the event timeline either for a peer (by peer label) or across all connected peers as a single unified timeline. Registry also allows clearing of events timeline on all connected instances through a single API call. See Registry APIs for additional info.

#### APIs

Param `reverse=y` produces the timeline in reverse chronological order. By default events are returned with `data` set to `...` to reduce the amount of data returned. Param `data=y` returns the events with data.

|METHOD|URI|Description|
|---|---|---|
| POST      | /events/flush    | Publish any pending events to the registry, and clear the instance's events timeline. |
| POST      | /events/clear    | Clear the instance's events timeline. |
| GET       | /events?reverse=`[y/n]`<br/>&data=`[y/n]` | Get events timeline of the instance. To get combined events from all instances, use the registry's peers events APIs instead.  |
| GET       | /events/search/`{text}`?<br/>reverse=`[y/n]`<br/>&data=`[y/n]` | Search the instance's events timeline. |


<details>
<summary>Server Events Details</summary>

Each `goto` instance publishes these events at startup and shutdown
- `Server Started`
- `GRPC Server Started`
- `Server Stopped`
- `GRPC Server Stopped`

A `goto` peer that's configured to connect to a `goto` registry publishes the following additional events at startup and shutdown:
- `Peer Registered`
- `Peer Startup Data`
- `Peer Deregistered`

A server generates `URI First Request` upon receiving the first request for a URI. Subsequent requests for that URI are tracked and counted as long as it produces the same response status. Once the response status code changes for a URI, it generates `Repeated URI Status` to log the accumulated summary of the URI so far, and the logs `URI Status Changed` to report the new status code. The accumulation and tracking logic then proceeds with this new status code, reporting once the status changes again for that URI.

Various other events are published by `goto` peer instances acting as client and server, and by the `goto` registry instance, which are listed in other sections in this Readme.

</details>

See [Events Example](../../docs/events-example.md)
