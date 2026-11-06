# Scripts Features

`Goto` allows scripts to be stored and executed on the `goto` server instance via APIs. (Scripts that are run by [Jobs](../job/README.md) are managed separately, via the `/jobs/add/script` and `/jobs/store/file` APIs.)


#### Scripts APIs
|METHOD|URI|Description|
|---|---|---|
| POST, PUT  |	/scripts/add/{name}     | Add an in-memory script under the given name. Request body payload is used as script content. Replaces an existing script of the same name. Responds `200` with `Script [name] added successfully`. |
| POST, PUT  |	/scripts/store/{name}     | Store the given script as a shell file under the given name. Request body payload is used as script content. The file is written as `{scripts dir}/{name}.sh` with mode `777` (the scripts dir is the `SCRIPTS_DIR` env var, or `{temp dir}/scripts` if not set; it is created if needed). Responds `200` with `Script [name] added successfully` (a failure to write the file is only logged, and the script then doesn't exist). |
| POST  |	/scripts/remove/{name} <br/> /scripts/{name}/remove  | Remove a script by name, and delete its file if it was stored as a file. Responds `200` with `Script [name] removed successfully`, also if there was no such script. |
| POST  |	/scripts/remove/all  | Remove all stored scripts (and delete their files). Responds `200` with `All scripts removed successfully`. |
| POST  |	/scripts/run/{name}?args={args} <br/> /scripts/{name}/run?args={args} | Run a script by name and with optional args. Delivers script output (stdout and stderr combined) as the response payload of this API call, with content type `application/octet-stream`. The request body is passed to the script as its standard input. Responds `404` with `Script [name] not found` for an unknown script. The API call waits until the script exits; the response is `200` even if the script fails, in which case the output ends with `Script: Failed to run script [...]. Error: [...]`. |
| GET  |	/scripts | Get all the stored scripts, as `{"<name>": {"name": "...", "filePath": "...", "content": null}}`. `filePath` is empty for in-memory scripts. (The `content` field is currently always `null`; the script text is not reported.) |

## Notes
- `{name}` - Script name
- `args` query parameter - Comma-separated list of arguments to pass to the script (the parameter can also be repeated, once per argument, e.g. `?args=a&args=b`)
- Script content should be provided in the request body for add/store operations
- Scripts added via `/add` are stored in memory only, and run as `sh -c "<script text>" <args...>`. Note that the first arg becomes `$0` of the script (so with `?args=a,b,c` the script sees `$0=a`, `$1=b`, `$2=c`).
- Scripts added via `/store` are persisted to disk, and run by executing the file directly with the args (`$1`, `$2`, ... as usual). The file is executed as is, so the content needs a shebang line (e.g. `#!/bin/sh`), else the run fails with `exec format error`.
- Only POST is accepted by the run and remove APIs.
- The [Pipeline](../pipe/README.md) feature registers its script sources in this same scripts registry, so they also show up in `GET /scripts`.
- The `--startupScript` command arg (it can be repeated) is meant to run commands at startup through the same script runner, as one `sh -c` script of the commands joined with `; `. Note: the call that runs it is currently disabled in the startup code, so the arg is accepted but the commands are not executed.
- The `scripts` section of a `goto ctl apply` config can upload and run scripts through these APIs (`/scripts/store/{name}` and `/scripts/run/{name}`). The startup config (`--config`) does not apply a `scripts` section.

#### Examples
```
# in-memory script, run with args and a request body used as stdin
curl -X POST localhost:8080/scripts/add/echoargs --data 'echo "arg1=$1 arg2=$2"; read line; echo "stdin=$line"'
curl -X POST 'localhost:8080/scripts/echoargs/run?args=a,b,c' --data 'input'
# arg1=b arg2=c
# stdin=input         (the first arg `a` is $0)

# stored script
curl -X POST localhost:8080/scripts/store/hello --data $'#!/bin/sh\necho "hello $@"'
curl -X POST 'localhost:8080/scripts/hello/run?args=world'
# hello world

curl localhost:8080/scripts
curl -X POST localhost:8080/scripts/hello/remove
curl -X POST localhost:8080/scripts/remove/all
```
