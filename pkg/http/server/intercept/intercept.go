package intercept

import "net/http"

type InterceptResponseWriter struct {
	http.ResponseWriter
	StatusCode int
	Data       []byte
	Hold       bool
}

func (rw *InterceptResponseWriter) WriteHeader(statusCode int) {
	rw.StatusCode = statusCode
	if !rw.Hold {
		rw.ResponseWriter.WriteHeader(statusCode)
	}
}

func (rw *InterceptResponseWriter) Write(b []byte) (int, error) {
	rw.Data = append(rw.Data, b...)
	if !rw.Hold {
		return rw.ResponseWriter.Write(b)
	}
	return 0, nil
}

func (rw *InterceptResponseWriter) Proceed() {
	if rw.Hold {
		if rw.StatusCode <= 0 {
			rw.StatusCode = 200
		}
		rw.ResponseWriter.WriteHeader(rw.StatusCode)
		rw.ResponseWriter.Write(rw.Data)
	}
}

func NewInterceptResponseWriter(w http.ResponseWriter, hold bool) *InterceptResponseWriter {
	return &InterceptResponseWriter{ResponseWriter: w, Hold: hold}
}
