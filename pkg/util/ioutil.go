/**
 * Copyright 2026 uk
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

package util

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"reflect"
	"regexp"
	"strings"
	"sync"
	"time"
)

var (
	replacer = strings.NewReplacer("[", "", "]", "", ".", "", ":", "", "/", "", "@", "-")
)

type Type interface {
	~string | ~bool | ~int | ~int64
}

// Ptr returns a pointer to a copy of t.
func Ptr[T Type](t T) *T { return &t }

type IReReader interface {
	Rewind()
}

type reader struct {
	ctx context.Context
	r   io.Reader
}

type ReReader struct {
	io.ReadCloser
	Content []byte
}

type ResponseTracker struct {
	io.ReadCloser
	resp       *http.Response
	onTrailers func(http.Header)
	once       sync.Once
}

// Read reads from the underlying reader but fails with the context error once the context is done.
func (r reader) Read(p []byte) (n int, err error) {
	if err = r.ctx.Err(); err != nil {
		return
	}
	if n, err = r.r.Read(p); err != nil {
		return
	}
	err = r.ctx.Err()
	return
}

// SetAndGetReReader replaces the request body with a re-readable ReReader and returns it.
func SetAndGetReReader(r *http.Request) *ReReader {
	rr := CreateOrGetReReader(r.Body)
	r.Body = rr
	return rr
}

// CreateOrGetReReader returns body if it is already a ReReader, otherwise buffers it into a new one.
func CreateOrGetReReader(body io.ReadCloser) *ReReader {
	var rr *ReReader
	if rr2, ok := body.(*ReReader); ok {
		rr = rr2
	} else {
		rr = NewReReader(body)
	}
	return rr
}

// NewReReader reads r fully into memory and returns a ReReader that can serve the content repeatedly.
func NewReReader(r io.ReadCloser) *ReReader {
	content := ReadBytes(r)
	return &ReReader{
		ReadCloser: io.NopCloser(bytes.NewReader(content)),
		Content:    content,
	}
}

// Rewind resets reading to the start of the buffered content.
func (r *ReReader) Rewind() {
	r.ReadCloser = io.NopCloser(bytes.NewReader(r.Content))
}

// Read reads from the current underlying reader.
func (r *ReReader) Read(p []byte) (n int, err error) {
	return r.ReadCloser.Read(p)
}

// Close closes the current reader but re-arms the ReReader with a fresh one over the content so it can be read again.
func (r *ReReader) Close() error {
	err := r.ReadCloser.Close()
	r.ReadCloser = ioutil.NopCloser(bytes.NewReader(r.Content))
	return err
}

// ReallyClose closes the underlying reader without re-arming it.
func (r *ReReader) ReallyClose() error {
	return r.ReadCloser.Close()
}

// Length returns the size of the buffered content in bytes.
func (r *ReReader) Length() int {
	return len(r.Content)
}

// Text returns the buffered content as a string.
func (r ReReader) Text() string {
	return string(r.Content)
}

// AsReReader returns r as a ReReader, reusing it if it already is one.
func AsReReader(r io.ReadCloser) *ReReader {
	if rr, ok := r.(*ReReader); ok {
		return rr
	}
	return CreateOrGetReReader(r)
}

// Reader wraps r so reads fail once ctx is done, also applying the context deadline as the read deadline when r supports it.
func Reader(ctx context.Context, r io.Reader) io.Reader {
	if deadline, ok := ctx.Deadline(); ok {
		type deadliner interface {
			SetReadDeadline(time.Time) error
		}
		if d, ok := r.(deadliner); ok {
			d.SetReadDeadline(deadline)
		}
	}
	return reader{ctx, r}
}

// NewResponseTracker wraps the response body so that onTrailers is called once with the headers merged with trailers when the body is fully read or closed.
func NewResponseTracker(resp *http.Response, onTrailers func(http.Header)) *ResponseTracker {
	return &ResponseTracker{
		ReadCloser: resp.Body,
		resp:       resp,
		onTrailers: onTrailers,
	}
}

// Read reads from the response body and triggers the trailers callback once at EOF.
func (rt *ResponseTracker) Read(p []byte) (n int, err error) {
	n, err = rt.ReadCloser.Read(p)
	if err == io.EOF {
		rt.once.Do(rt.onClose)
	}
	return
}

// Close drains and closes the response body and triggers the trailers callback once.
func (rt *ResponseTracker) Close() error {
	if rt.resp != nil {
		io.Copy(io.Discard, rt.ReadCloser)
		err := rt.ReadCloser.Close()
		rt.once.Do(rt.onClose)
		return err
	}
	return nil
}

// onClose merges the response trailers into its headers and passes them to the onTrailers callback.
func (rt *ResponseTracker) onClose() {
	for k, v := range rt.resp.Trailer {
		rt.resp.Header[k] = append(rt.resp.Header[k], v...)
	}
	rt.onTrailers(rt.resp.Header)
}

// BuildFilePath joins a directory and file name, adding a path separator when needed.
func BuildFilePath(filePath, fileName string) string {
	if filePath != "" && !strings.HasSuffix(filePath, "/") && !strings.HasSuffix(filePath, "\\") {
		filePath += string(os.PathSeparator)
	}
	filePath += fileName
	return filePath
}

// StoreFile writes content to fileName under filePath (creating the directory) and returns the full path, doing nothing for an empty name.
func StoreFile(filePath, fileName string, content []byte) (string, error) {
	if fileName == "" {
		return "", nil
	}
	if _, err := os.Stat(filePath); os.IsNotExist(err) {
		os.MkdirAll(filePath, os.ModePerm)
	}
	filePath = BuildFilePath(filePath, fileName)
	if err := os.WriteFile(filePath, content, 0777); err == nil {
		return filePath, nil
	} else {
		return "", err
	}
}

// LoadFile reads the file as a string, with an error if the name is empty or the file doesn't exist.
func LoadFile(filePath string) (string, error) {
	if filePath == "" {
		return "", fmt.Errorf("no filename")
	}
	if _, err := os.Stat(filePath); os.IsNotExist(err) {
		return "", fmt.Errorf("file doesn't exist")
	}
	if content, err := os.ReadFile(filePath); err == nil {
		return string(content), nil
	} else {
		return "", err
	}
}

// Read reads r fully and returns the content with surrounding spaces trimmed, or empty on error.
func Read(r io.ReadCloser) string {
	if body, err := io.ReadAll(r); err == nil {
		return strings.Trim(string(body), " ")
	} else {
		log.Println(err.Error())
	}
	return ""
}

// ReadBytes reads r fully, returning nil on error.
func ReadBytes(r io.Reader) []byte {
	if body, err := io.ReadAll(r); err == nil {
		return body
	} else {
		log.Println(err.Error())
	}
	return nil
}

// ReadArrayOfArrays reads a JSON array and returns each element re-marshaled as bytes, or the raw content as one element if it isn't a JSON array.
func ReadArrayOfArrays(r io.Reader) [][]byte {
	b := ReadBytes(r)
	var arr []interface{}
	var data [][]byte
	err := json.Unmarshal(b, &arr)
	if err != nil {
		return [][]byte{b}
	}
	for _, a2 := range arr {
		if arr2, ok := a2.([]interface{}); ok {
			b2, err := json.Marshal(arr2)
			if err != nil {
				log.Println(err.Error())
				return nil
			}
			data = append(data, b2)
		} else {
			b2, err := json.Marshal(a2)
			if err != nil {
				log.Println(err.Error())
				return nil
			}
			data = append(data, b2)
		}
	}
	return data
}

// ReadAndTrack reads r to EOF, optionally collecting the data and copying it to w, returning the data, total size, first and last read times and any error text.
func ReadAndTrack(r io.Reader, collect bool, w io.Writer) ([]byte, int, time.Time, time.Time, string) {
	buf := make([]byte, 1000)
	var result []byte
	var readSize int
	var first, last time.Time
	canWrite := w != nil && !reflect.ValueOf(w).IsNil()
	for {
		size, err := r.Read(buf)
		if err == nil || err == io.EOF {
			now := time.Now()
			if first.IsZero() {
				first = now
			}
			last = now
			readSize += size
			data := buf[0:size]
			if collect {
				result = append(result, data...)
			}
			if canWrite {
				if _, err2 := w.Write(data); err2 != nil {
					return result, readSize, first, last, err2.Error()
				}
			}
		}
		if err == io.EOF {
			return result, readSize, first, last, ""
		} else if err != nil {
			return result, readSize, first, last, err.Error()
		}
	}
}

// WriteAndTrack writes each data chunk to w in order, retrying partial writes and pausing delay between chunks, then closes w and returns the size written, first and last write times and any error.
func WriteAndTrack(w io.WriteCloser, data [][]byte, delay time.Duration) (int, time.Time, time.Time, error) {
	defer w.Close()
	count := len(data)
	var writeSize int
	var first, last time.Time
	for i := 0; i < count; i++ {
		d := data[i]
		size := len(d)
		for {
			n, err := w.Write(d)
			now := time.Now()
			if first.IsZero() {
				first = now
			}
			last = now
			writeSize += n
			if err != nil {
				return writeSize, first, last, err
			}
			if n >= size {
				break
			}
		}
		if i < count-1 && delay > 0 {
			time.Sleep(delay)
		}
	}
	return writeSize, first, last, nil
}

// StringArrayContains reports whether any string in list matches the regexp.
func StringArrayContains(list []string, r *regexp.Regexp) bool {
	for _, v := range list {
		if r.MatchString(v) {
			return true
		}
	}
	return false
}

// IsStringInArray reports whether val matches any entry of list, each treated as a regular expression.
func IsStringInArray(val string, list []string) bool {
	b := []byte(val)
	for _, v := range list {
		if matched, _ := regexp.Match(v, b); matched {
			return true
		}
	}
	return false
}

// GetMapKeys returns the keys of a map (via reflection) as strings, or an empty list if m isn't a map.
func GetMapKeys(m interface{}) []string {
	keys := []string{}
	if v := reflect.ValueOf(m); v.Kind() == reflect.Map {
		for _, key := range v.MapKeys() {
			keys = append(keys, fmt.Sprint(key))
		}
	}
	return keys
}

// LowerAndTrim lowercases s and trims surrounding spaces.
func LowerAndTrim(s string) string {
	return strings.ToLower(strings.Trim(s, " "))
}

// GetCwd returns the current working directory, or "." if it can't be determined.
func GetCwd() string {
	if cwd, err := os.Getwd(); err != nil {
		log.Printf("Failed to get current directory with error: %s", err.Error())
		return "."
	} else {
		return cwd
	}
}

// EmptyBody returns an empty reader to use as a request or response body.
func EmptyBody() io.Reader {
	return io.NopCloser(strings.NewReader(""))
}

// Sanitize removes the characters [ ] . : / from name and replaces @ with -, making it safe for use as a key or file name.
func Sanitize(name string) string {
	return replacer.Replace(name)
}

// LinkChannels forwards every value received from one channel to the other until the source channel is closed.
func LinkChannels[T any](from, to chan T) {
	for v := range from {
		to <- v
	}
	log.Println("Channel Link Finished")
}
