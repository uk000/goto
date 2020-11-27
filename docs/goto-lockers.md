# Goto Lockers

`Goto` provides `Labeled lockers` as general purpose in-memory key-value store. A locker can be created using `/open` API and removed using `/close`. Each instance starts with a default locker (label `default`). At any point, one locker remains current (can be referenced in various locker APIs using label `current`). Opening a new locker makes it the current locker. Closing a locker removes it from the memory. If the current locker is removed, the `default` locker becomes current again. Calling `/open` for an existing locker makes it the current locker again. All lockers are accessible using their `labels` until they are closed.

A locker consists of two parts: 
- `Peer Lockers`: used to store results reported by various peer `goto` instances
- `Data Locker`: available for custom data storage/retrieval using `/store`, `/get` and `/find` APIs.

<img src="Goto-Lockers.png" width="600" height="450" />

<br/>
<br/>

## Data Lockers
A `Data Locker` is a recursive key-value storage structure. Each key may hold some `data` as text, and/or hold a set of `subkeys`. Each subkey in turn holds a `Data Locker` structure of its own, capable of holding data as well as subkeys, and the structure keeps recursing. The recursive tree is built on the fly based on the key paths added via the `/store` APIs.

The `/store` and `/get` APIs hide the complexity of the recursive storage by providing a simple interface that takes all the component keys of a path as comma-separated list, and creates/reads data at/from the given path. E.g. `/lockers/current/store/A,B,C` stores the given payload (POST) in the data field at the leaf (i.e. at node `C`) key path `A -> B -> C`. 

Similarly, `/lockers/X/get/A,B,C` reads data stored under subkey `C` under path `A -> B -> C` in locker `X`. 

If no data was stored under the given path, the `/get` API returns the sub-locker tree at that node (as JSON). Thus, the same API can be used to read a specific key's data as text or an entire sub-tree of a key. 

E.g. given the following store calls were executed: 
- `curl http://goto:8080/registry/lockers/X/store/A,B --data 'B data'`
- `curl http://goto:8080/registry/lockers/X/store/A,C --data 'C data'`

resulting in data being stored under keys `B` and `C` under common parent key `A`.

Then, a call to `curl http://goto:8080/registry/lockers/X/get/A` will return A's subtree (as JSON), which includes subkeys `B` and `C` and their corresponding sublockers. Whereas, a call to `curl http://goto:8080/registry/lockers/X/get/A,B` will return the text `B data` stored under key `B` under parent `A`.

Thus just a small set of APIs can be used in several flexible ways, making the lockers a useful data store for testing purposes (store results, logs, etc.).

<br/>

## Peer Lockers

`Peer Lockers` store data organized by peer labels (as reported by the peer instances). For each peer label, a sublocker is created for each instance of the peer that connects to the registry. Each instance sublocker holds a recursive data structure similar to `Data Locker` . 

When the peers invoke some traffic and collect results, they send their invocations results to the registry in real-time, and registry stores each instance's results in the instance's corresponding instance locker.

<img src="Goto-Lockers-Peers.png" width="500" height="500" />

Invocation results reported by various peer clients can be viewed in processed form (summary and detailed) using various `/results` API instead (e.g. `/lockers/targets/results` and `/lockers/targets/results?detailed=y`). Raw data from peer lockers can be viewed using various `/get` or `/dump` APIs.


See [Registry Lockers APIs](../README.md#registry-lockers-apis)