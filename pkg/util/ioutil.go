/**
 * Copyright 2025 uk
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
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"os"
	"reflect"
	"regexp"
	"strings"
	"time"
)

type reader struct {
	ctx context.Context
	r   io.Reader
}

type ReReader struct {
	io.ReadCloser
	Content []byte
}

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

func NewReReader(r io.ReadCloser) *ReReader {
	content := ReadBytes(r)
	return &ReReader{
		ReadCloser: ioutil.NopCloser(bytes.NewReader(content)),
		Content:    content,
	}
}

func (r *ReReader) Read(p []byte) (n int, err error) {
	return r.ReadCloser.Read(p)
}

func (r *ReReader) Close() error {
	err := r.ReadCloser.Close()
	r.ReadCloser = ioutil.NopCloser(bytes.NewReader(r.Content))
	return err
}

func (r *ReReader) ReallyClose() error {
	return r.ReadCloser.Close()
}

func AsReReader(r io.ReadCloser) *ReReader {
	if rr, ok := r.(*ReReader); ok {
		return rr
	}
	return NewReReader(r)
}

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

func BuildFilePath(filePath, fileName string) string {
	if filePath != "" && !strings.HasSuffix(filePath, "/") {
		filePath += "/"
	}
	filePath += fileName
	return filePath
}

func StoreFile(filePath, fileName string, content []byte) (string, error) {
	if fileName == "" {
		return "", nil
	}
	if _, err := os.Stat(filePath); os.IsNotExist(err) {
		os.MkdirAll(filePath, os.ModePerm)
	}
	filePath = BuildFilePath(filePath, fileName)
	if err := ioutil.WriteFile(filePath, content, 0777); err == nil {
		return filePath, nil
	} else {
		return "", err
	}
}

func Read(r io.ReadCloser) string {
	if body, err := ioutil.ReadAll(r); err == nil {
		r.Close()
		return strings.Trim(string(body), " ")
	} else {
		log.Println(err.Error())
	}
	return ""
}

func ReadBytes(r io.Reader) []byte {
	if body, err := ioutil.ReadAll(r); err == nil {
		return body
	} else {
		log.Println(err.Error())
	}
	return nil
}

func ReadAndTrack(r io.Reader, collect bool) ([]byte, int, time.Time, time.Time, string) {
	buf := make([]byte, 1000)
	var result []byte
	var readSize int
	var first, last time.Time
	for {
		size, err := r.Read(buf)
		now := time.Now()
		if first.IsZero() {
			first = now
		}
		last = now
		readSize += size
		if collect {
			result = append(result, buf[0:size]...)
		}
		if err == io.EOF {
			return result, readSize, first, last, ""
		} else if err != nil {
			return result, readSize, first, last, err.Error()
		}
	}
}

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

func StringArrayContains(list []string, r *regexp.Regexp) bool {
	for _, v := range list {
		if r.MatchString(v) {
			return true
		}
	}
	return false
}

func IsStringInArray(val string, list []string) bool {
	b := []byte(val)
	for _, v := range list {
		if matched, _ := regexp.Match(v, b); matched {
			return true
		}
	}
	return false
}

func GetMapKeys(m interface{}) []string {
	keys := []string{}
	if v := reflect.ValueOf(m); v.Kind() == reflect.Map {
		for _, key := range v.MapKeys() {
			keys = append(keys, fmt.Sprint(key))
		}
	}
	return keys
}

func LowerAndTrim(s string) string {
	return strings.ToLower(strings.Trim(s, " "))
}

func GetCwd() string {
	if cwd, err := os.Getwd(); err != nil {
		log.Printf("Failed to get current directory with error: %s", err.Error())
		return "."
	} else {
		return cwd
	}
}
