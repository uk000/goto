package stream

import (
	"fmt"
	"goto/pkg/constants"
	"goto/pkg/server/conn"
	"goto/pkg/server/intercept"
	"goto/pkg/server/middleware"
	"goto/pkg/server/response/payload"
	"goto/pkg/types"
	"goto/pkg/util"
	"log"
	"net/http"
	"strconv"
	"time"

	"github.com/gorilla/mux"
)

var (
	Middleware = middleware.NewMiddleware("response.payload", setRoutes, nil)
)

func setRoutes(r *mux.Router) {
	streamRouter := util.PathRouter(r, "/stream")
	util.AddRouteWithMultiQ(streamRouter, "", streamResponse, [][]string{{"count"}, {"delay"}, {"chunksize"}}, "GET", "PUT", "POST")
	util.AddRouteWithMultiQ(streamRouter, "", streamResponse, [][]string{{}, {"duration"}, {"delay"}, {"chunksize"}}, "GET", "PUT", "POST")
}

func streamResponse(w http.ResponseWriter, r *http.Request) {
	chunkSize := util.GetSizeParam(r, "chunksize")
	durMin, durMax, _, _ := util.GetDurationParam(r, "duration")
	delayText := util.GetStringParamValue(r, "delay")
	delayMin, delayMax, _, _ := util.GetDurationParam(r, "delay")
	count := util.GetIntParamValue(r, "count")
	repeat := false
	data := util.ReadBytes(r.Body)
	contentType := r.Header.Get(constants.HeaderContentType)
	if contentType == "" {
		contentType = "plain/text"
	}
	duration := types.RandomDuration(durMin, durMax)
	delay := types.RandomDuration(delayMin, delayMax)
	if delay == 0 {
		delay = 1 * time.Second
	}
	if duration > 0 {
		count = int(duration.Milliseconds()/delay.Milliseconds()) + 1
	}
	if chunkSize == 0 {
		chunkSize = 10
	}
	if count == 0 {
		count = 10
	}
	if len(data) == 0 {
		data = types.GenerateRandomPayload(chunkSize, util.GetCurrentListenerLabel(r))
		repeat = true
	} else if len(data) < chunkSize {
		data = payload.FixPayload(data, chunkSize)
	}
	w.Header().Set(constants.HeaderContentType, contentType)
	w.Header().Set(constants.HeaderXContentTypeOptions, "nosniff")
	w.Header().Set(constants.HeaderGotoChunkCount, strconv.Itoa(count))
	w.Header().Set(constants.HeaderGotoChunkLength, strconv.Itoa(chunkSize))
	w.Header().Set(constants.HeaderGotoChunkDelay, delayText)
	if count > 0 && chunkSize > 0 {
		w.Header().Set(constants.HeaderGotoStreamLength, strconv.Itoa(chunkSize*count))
	}
	if duration > 0 {
		w.Header().Set(constants.HeaderGotoStreamDuration, duration.String())
	}

	fw := intercept.NewFlushWriter(w)
	if fw == nil {
		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprintln(w, "Cannot stream")
		return
	}
	if c := conn.GetConn(r); c != nil {
		c.SetWriteDeadline(time.Time{})
	}
	util.AddLogMessage("Responding with streaming payload", r)
	payloadIndex := 0
	payloadSize := len(data)
	payloadChunkCount := payloadSize / chunkSize
	if payloadSize%chunkSize > 0 {
		payloadChunkCount++
	}
	for i := 0; i < count; i++ {
		start := payloadIndex * chunkSize
		end := (payloadIndex + 1) * chunkSize
		if end > payloadSize {
			end = payloadSize
		}
		chunkData := data[start:end]
		chunkData = append(chunkData, []byte("\n")...)
		if _, err := fw.Write(chunkData); err != nil {
			log.Printf("Failed to write stream response with error: %s", err.Error())
			fmt.Fprintf(w, "Failed to write stream response with error: %s", err.Error())
			return
		}
		payloadIndex++
		if payloadIndex == payloadChunkCount {
			if repeat {
				payloadIndex = 0
			} else {
				break
			}
		}
		if i < count-1 {
			delay = types.RandomDuration(delayMin, delayMax, delay)
			time.Sleep(delay)
		}
	}
}
