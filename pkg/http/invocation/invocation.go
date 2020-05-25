package invocation

import (
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"net"
	"net/http"
	"reflect"
	"strconv"
	"strings"
	"time"
)

type InvocationSpec struct {
	Name                  string
	Method                string
	Url                   string
	Headers               [][]string
	Body                  string
	BodyReader            io.Reader
	Replicas              int
	RequestCount          int
	Delay                 string
	delayD                time.Duration
	KeepOpen              string
	keepOpenD             time.Duration
	SendId                bool
	completedRequestCount int
}

type InvocationResult struct {
	TargetName string
	TargetId   string
	Status     string
	StatusCode int
	Headers    map[string][]string
	Body       string
}

func ValidateSpec(spec *InvocationSpec) error {
	if spec.Name == "" {
		return fmt.Errorf("Name is required")
	}
	if spec.Method == "" {
		return fmt.Errorf("Method is required")
	}
	if spec.Url == "" {
		return fmt.Errorf("URL is required")
	}
	if spec.Replicas < 0 {
		return fmt.Errorf("Invalid replicas")
	} else if spec.Replicas == 0 {
		spec.Replicas = 1
	}
	if spec.RequestCount < 0 {
		return fmt.Errorf("Invalid requestCount")
	} else if spec.RequestCount == 0 {
		spec.RequestCount = 1
	}
	var err error
	if spec.Delay != "" {
		if spec.delayD, err = time.ParseDuration(spec.Delay); err != nil {
			return fmt.Errorf("Invalid delay")
		}
	} else {
		spec.delayD = 10 * time.Millisecond
	}
	if spec.KeepOpen != "" {
		if spec.keepOpenD, err = time.ParseDuration(spec.KeepOpen); err != nil {
			return fmt.Errorf("Invalid keepOpen")
		}
	}
	if spec.BodyReader != nil && spec.Body == "" && spec.Replicas > 1 {
		return fmt.Errorf("Streaming request body can only be forwarded to one target whereas replicas is %d", spec.Replicas)
	}
	return nil
}

func InvokeTargets(targets []*InvocationSpec, reportBody bool) []*InvocationResult {
	var responses []*InvocationResult
	if len(targets) > 0 {
		targetCount := 0
		for _, target := range targets {
			targetCount += (target.Replicas * target.RequestCount)
			target.completedRequestCount = 0
		}
		responseChannels := make([]chan *InvocationResult, targetCount)
		cases := make([]reflect.SelectCase, targetCount)
		index := 0
		for {
			delay := 10 * time.Millisecond
			for _, target := range targets {
				if target.completedRequestCount < target.RequestCount {
					if target.delayD > delay {
						delay = target.delayD
					}
					for i := 0; i < target.Replicas; i++ {
						targetReplicaIndex := (target.completedRequestCount * target.Replicas) + i + 1
						targetCount--
						responseChannels[index] = make(chan *InvocationResult)
						cases[index] = reflect.SelectCase{Dir: reflect.SelectRecv, Chan: reflect.ValueOf(responseChannels[index])}
						bodyReader := target.BodyReader
						if i > 0 {
							bodyReader = strings.NewReader(target.Body)
						}
						targetId := target.Name + "[" + strconv.Itoa(targetReplicaIndex) + "]"
						url := target.Url
						if target.SendId {
							if !strings.Contains(url, "?") {
								url += "?"
							} else if strings.Contains(url, "&") {
								url += "&"
							}
							url += "x-request-id="
							url += targetId
						}
						go InvokeTarget(target.Name, targetId, url, target.Method, target.Headers, bodyReader, reportBody, responseChannels[index])
						index++
					}
					target.completedRequestCount++
				}
			}
			if targetCount == 0 {
				break
			}
			time.Sleep(delay)
		}
		for len(cases) > 0 {
			i, v, _ := reflect.Select(cases)
			cases = append(cases[:i], cases[i+1:]...)
			responses = append(responses, v.Interface().(*InvocationResult))
		}
	}
	return responses
}

func InvokeTarget(targetName string, targetId string, url string, method string, headers [][]string, body io.Reader, reportBody bool, c chan *InvocationResult) {
	defer close(c)
	tr := &http.Transport{
		MaxIdleConns:       1,
		IdleConnTimeout:    30 * time.Second,
		DisableCompression: true,
		Proxy:              http.ProxyFromEnvironment,
		Dial: (&net.Dialer{
			Timeout:   30 * time.Second,
			KeepAlive: time.Minute,
		}).Dial,
		TLSHandshakeTimeout: 10 * time.Second,
	}
	client := &http.Client{Transport: tr}
	var response InvocationResult
	response.TargetName = targetName
	response.TargetId = targetId
	response.Headers = map[string][]string{}
	if req, err := http.NewRequest(method, url, body); err == nil {
		for _, h := range headers {
			if strings.EqualFold(h[0], "host") {
				req.Host = h[1]
			} else {
				req.Header.Add(h[0], h[1])
			}
		}
		if resp, err := client.Do(req); err == nil {
			defer resp.Body.Close()
			for header, values := range resp.Header {
				response.Headers[header] = values
			}
			response.Headers["Status"] = []string{resp.Status}
			response.Status = resp.Status
			response.StatusCode = resp.StatusCode
			if reportBody {
				if body, err := ioutil.ReadAll(resp.Body); err == nil {
					response.Body = string(body)
				}
			}
			c <- &response
		} else {
			log.Printf("Target %s invocation failed with error: %s\n", targetId, err.Error())
			response.Status = err.Error()
			c <- &response
		}
	} else {
		log.Printf("Target %s failed to create request with error: %s\n", targetId, err.Error())
		response.Status = err.Error()
		c <- &response
	}
}
