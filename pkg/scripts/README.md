# Scripts Features

`Goto` allows scripts to be stored and executed on the `goto` server instance via APIs.


#### Scripts APIs
|METHOD|URI|Description|
|---|---|---|
| POST, PUT  |	/scripts/add/{name}     | Add an in-memory script under the given name. Request body payload is used as script content. |
| POST, PUT  |	/scripts/store/{name}     | Store the given script as a shell file under the given name. Request body payload is used as script content. |
| POST, PUT  |	/scripts/remove/{name} <br/> /scripts/{name}/remove  | Remove a script by name. |
| POST, PUT  |	/scripts/remove/all  | Remove all stored scripts. |
| POST, PUT  |	/scripts/run/{name}?args={args} <br/> /scripts/{name}/run?args={args} | Run a script by name and with optional args. Delivers script output as the response payload of this API call. |
| GET  |	/scripts | Get all the stored scripts with their content (lines of a script are delivered as 

## Notes
- `{name}` - Script name
- `args` query parameter - Comma-separated list of arguments to pass to the script
- Script content should be provided in the request body for add/store operations
- Scripts added via `/add` are stored in memory only
- Scripts added via `/store` are persisted to disk
