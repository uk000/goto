package memory

import (
	"fmt"
	"goto/pkg/server/middleware"
	"goto/pkg/util"
	"io"
	"net/http"

	"github.com/gorilla/mux"
)

var (
	Middleware = middleware.NewMiddleware("memory", setRoutes, middlewareFunc)
)

func setRoutes(r *mux.Router, parent *mux.Router, root *mux.Router) {
	memoryRouter := util.PathRouter(r, "/memory")

	util.AddRouteWithPort(memoryRouter, "/contexts/add/{context}", addContext, "POST")
	util.AddRouteWithPort(memoryRouter, "/context/{context}/add/{key}={value}", addItem, "POST")

	util.AddRouteWithPort(memoryRouter, "/context/{ctxkey}/from/header/{header}", addContextMatch, "POST")
	util.AddRouteQWithPort(memoryRouter, "/context/{ctxkey}/from/uri", addContextMatch, "uri", "POST")

	util.AddRouteWithPort(memoryRouter, "/context/{ctxkey}/memory/from/header/{header}", addHeaderQueryMatch, "POST")
	util.AddRouteWithPort(memoryRouter, "/context/{ctxkey}/memory/from/query/{query}", addHeaderQueryMatch, "POST")
	util.AddRouteWithPort(memoryRouter, "/context/{ctxkey}/memory/from/body/regex/{key}={regex}", addBodyRegexMatch, "POST")
	util.AddRouteWithPort(memoryRouter, "/context/{ctxkey}/memory/from/body/paths/{paths}", addBodyJSONPathMatch, "POST")
	util.AddRouteWithPort(memoryRouter, "/context/{ctxkey}/memory/from/body/transform", setPayloadTransform, "POST")

	// util.AddRouteWithPort(memoryRouter, "/context/{ctxkey}/memory/apply/on/header/{header}/add/{header2}/{key}", applyHeaderFromMemory, "POST")
	// util.AddRouteWithPort(memoryRouter, "/context/{ctxkey}/memory/apply/on/header/{header}/replace/{header2}/{key}", applyHeaderFromMemory, "POST")
	// util.AddRouteWithPort(memoryRouter, "/context/{ctxkey}/memory/apply/on/header/{header}/set/value/{key}", applyHeaderFromMemory, "POST")

	// util.AddRouteWithPort(memoryRouter, "/context/{ctxkey}/memory/apply/on/query/{query}/add/{query2}/{key}", applyQueryFromMemory, "POST")
	// util.AddRouteWithPort(memoryRouter, "/context/{ctxkey}/memory/apply/on/query/{query}/replace/{query2}/{key}", applyQueryFromMemory, "POST")
	// util.AddRouteWithPort(memoryRouter, "/context/{ctxkey}/memory/apply/on/query/{query}/set/value/{key}", applyQueryFromMemory, "POST")

	// util.AddRouteWithPort(memoryRouter, "/context/{ctxkey}/memory/apply/on/body/regex/{regex}/add/{regex2}/{key}", applyBodyRegexFromMemory, "POST")
	// util.AddRouteWithPort(memoryRouter, "/context/{ctxkey}/memory/apply/on/body/regex/{regex}/replace/{regex2}/{key}", applyBodyRegexFromMemory, "POST")
	// util.AddRouteWithPort(memoryRouter, "/context/{ctxkey}/memory/apply/on/body/regex/{regex}/set/{key}", applyBodyRegexFromMemory, "POST")

	// util.AddRouteWithPort(memoryRouter, "/context/{ctxkey}/memory/apply/on/body/path/{path}/add/{path2}/{key}", applyBodyPathFromMemory, "POST")
	// util.AddRouteWithPort(memoryRouter, "/context/{ctxkey}/memory/apply/on/body/path/{path}/replace/{path2}/{key}", applyBodyPathFromMemory, "POST")
	// util.AddRouteWithPort(memoryRouter, "/context/{ctxkey}/memory/apply/on/body/path/{path}/set/value/{key}", applyBodyPathFromMemory, "POST")

	util.AddRouteWithPort(memoryRouter, "/contexts/memory", getMemory, "GET")
	util.AddRouteWithPort(memoryRouter, "/context/{context}/memory", getMemory, "GET")
	util.AddRouteWithPort(memoryRouter, "/context/{context}/memory/get/{key}", getMemory, "GET")
	util.AddRouteWithPort(memoryRouter, "/context/{ctxkey}", getMemoryExtractors, "GET")
	util.AddRouteWithPort(memoryRouter, "/contexts", getMemoryExtractors, "GET")

}

func addContext(w http.ResponseWriter, r *http.Request) {
	port := util.GetRequestOrListenerPortNum(r)
	ctx := util.GetStringParamValue(r, "context")
	msg := ""
	if ctx != "" {
		GetMemoryManager(port).AddContext(ctx)
		msg = fmt.Sprintf("Added Context [%s] to memory of port [%d]", ctx, port)
	} else {
		w.WriteHeader(http.StatusBadRequest)
		msg = "Missing context"
	}
	fmt.Fprintln(w, msg)
	util.AddLogMessage(msg, r)
}

func addItem(w http.ResponseWriter, r *http.Request) {
	port := util.GetRequestOrListenerPortNum(r)
	ctx := util.GetStringParamValue(r, "context")
	key := util.GetStringParamValue(r, "key")
	value := util.GetStringParamValue(r, "value")
	if value == "" {
		if b, err := io.ReadAll(r.Body); err == nil {
			value = string(b)
		}
	}
	msg := ""
	if ctx != "" && key != "" && value != "" {
		GetMemoryManager(port).AddContext(ctx).Add(key, value)
		msg = fmt.Sprintf("Stored [%s: %s] into Context [%s] in memory of port [%d]", key, value, ctx, port)
	} else {
		w.WriteHeader(http.StatusBadRequest)
		msg = "Missing context/key/value"
	}
	fmt.Fprintln(w, msg)
	util.AddLogMessage(msg, r)
}

func addContextMatch(w http.ResponseWriter, r *http.Request) {
	port := util.GetRequestOrListenerPortNum(r)
	ctxkey := util.GetStringParamValue(r, "ctxkey")
	header := util.GetStringParamValue(r, "header")
	msg := ""
	if header != "" {
		GetMemoryManager(port).AddContextMatch(ctxkey, header)
		msg = fmt.Sprintf("Added Context Key [%s] Match on header [%s] for memory context on port [%d]", ctxkey, header, port)
	} else {
		w.WriteHeader(http.StatusBadRequest)
		msg = "Missing header key"
	}
	fmt.Fprintln(w, msg)
	util.AddLogMessage(msg, r)
}

func addHeaderQueryMatch(w http.ResponseWriter, r *http.Request) {
	port := util.GetRequestOrListenerPortNum(r)
	ctxkey := util.GetStringParamValue(r, "ctxkey")
	header := util.GetStringParamValue(r, "header")
	query := util.GetStringParamValue(r, "query")
	m := GetMemoryManager(port)
	msg := ""
	if header != "" {
		m.AddHeaderMatch(ctxkey, header)
		msg = fmt.Sprintf("Added Context Key [%s] Memory Extraction from Header [%s] for memory of port [%d]", ctxkey, header, port)
	} else if query != "" {
		m.AddQueryMatch(ctxkey, query)
		msg = fmt.Sprintf("Added Context Key [%s] Memory Extraction from Query [%s] for memory of port [%d]", ctxkey, query, port)
	} else {
		w.WriteHeader(http.StatusBadRequest)
		msg = "Missing header/query key"
	}
	fmt.Fprintln(w, msg)
	util.AddLogMessage(msg, r)
}

func addBodyRegexMatch(w http.ResponseWriter, r *http.Request) {
	port := util.GetRequestOrListenerPortNum(r)
	ctxkey := util.GetStringParamValue(r, "ctxkey")
	key := util.GetStringParamValue(r, "key")
	regex := util.GetStringParamValue(r, "regex")
	m := GetMemoryManager(port)
	msg := ""
	if key != "" && regex != "" {
		m.AddBodyRegexMatch(ctxkey, key, regex)
		msg = fmt.Sprintf("Added Context Key [%s] Memory Extraction from Body Regex match [%s: %s] for memory of port [%d]", ctxkey, key, regex, port)
	} else {
		w.WriteHeader(http.StatusBadRequest)
		msg = "Missing key or regex"
	}
	fmt.Fprintln(w, msg)
	util.AddLogMessage(msg, r)
}

func addBodyJSONPathMatch(w http.ResponseWriter, r *http.Request) {
	port := util.GetRequestOrListenerPortNum(r)
	ctxkey := util.GetStringParamValue(r, "ctxkey")
	paths := util.GetStringParamValue(r, "paths")
	m := GetMemoryManager(port)
	msg := ""
	if paths != "" {
		m.AddBodyJsonPathMatch(ctxkey, paths)
		msg = fmt.Sprintf("Added Context Key [%s] Memory Extraction from Body Json Path match [%s] for memory of port [%d]", ctxkey, paths, port)
	} else {
		w.WriteHeader(http.StatusBadRequest)
		msg = "Missing body match criteria"
	}
	fmt.Fprintln(w, msg)
	util.AddLogMessage(msg, r)
}

func setPayloadTransform(w http.ResponseWriter, r *http.Request) {
	port := util.GetRequestOrListenerPortNum(r)
	msg := ""
	var transforms []*util.Transform
	if err := util.ReadJsonPayload(r, &transforms); err == nil {
		m := GetMemoryManager(port)
		ctxkey := util.GetStringParamValue(r, "ctxkey")
		m.SetBodyTransformation(ctxkey, transforms)
		msg = fmt.Sprintf("Added Context Key [%s] Pre-Extraction Body Transform match [%s] for memory of port [%d]", ctxkey, util.ToJSONText(transforms), port)
	} else {
		w.WriteHeader(http.StatusBadRequest)
		msg = "Invalid payload"
	}
	fmt.Fprintln(w, msg)
	util.AddLogMessage(msg, r)
}

// func applyHeaderFromMemory(w http.ResponseWriter, r *http.Request) {
// 	port := util.GetRequestOrListenerPortNum(r)
// 	ctxkey := util.GetStringParamValue(r, "ctxkey")
// 	header := util.GetStringParamValue(r, "header")
// 	header2 := util.GetStringParamValue(r, "header2")
// 	key := util.GetStringParamValue(r, "key")
// 	msg := ""
// 	defer func() {
// 		fmt.Fprintln(w, msg)
// 		util.AddLogMessage(msg, r)
// 	}()
// 	if header == "" || key == "" {
// 		msg = "Missing required values: header/key"
// 		return
// 	}
// 	m := GetMemoryManager(port)
// 	if header != "" {
// 		m.AddHeaderMatch(ctxkey, header)
// 		msg = fmt.Sprintf("Added Context Key [%s] Memory Extraction from Header [%s] for memory of port [%d]", ctxkey, header, port)
// 	} else if query != "" {
// 		m.AddQueryMatch(ctxkey, query)
// 		msg = fmt.Sprintf("Added Context Key [%s] Memory Extraction from Query [%s] for memory of port [%d]", ctxkey, query, port)
// 	} else {
// 		w.WriteHeader(http.StatusBadRequest)
// 		msg = "Missing header/query key"
// 	}
// }

func getMemoryExtractors(w http.ResponseWriter, r *http.Request) {
	port := util.GetRequestOrListenerPortNum(r)
	ctxKey := util.GetStringParamValue(r, "ctxkey")
	data := GetMemoryManager(port).GetContextExtractors(ctxKey)
	util.WriteJsonPayload(w, data)
	util.AddLogMessage("Reported Context Extractors", r)
}

func getMemory(w http.ResponseWriter, r *http.Request) {
	port := util.GetRequestOrListenerPortNum(r)
	ctx := util.GetStringParamValue(r, "context")
	key := util.GetStringParamValue(r, "key")
	if ctx == "-" {
		ctx = ""
	}
	mm := GetMemoryManager(port)
	memory := mm.GetOrAddContext(ctx)
	if key != "" {
		fmt.Fprintln(w, memory.Get(key))
	} else if ctx != "" {
		util.WriteJsonPayload(w, memory)
	} else {
		util.WriteJsonPayload(w, mm.ContextMemory)
	}
	util.AddLogMessage(fmt.Sprintf("Reported Memory for Context [%s] Key [%s]", ctx, key), r)
}
