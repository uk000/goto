package ctl

type Traffic struct {
	Config *TrafficConfig `yaml:"config,omitempty"`
	Invoke []string       `yaml:"invoke,omitempty"`
}

type TrafficConfig struct {
	Tracking TrafficTrackConfig `yaml:"tracking,omitempty"`
	Targets  []TrafficTarget    `yaml:"targets"`
}

type TrafficTrackConfig struct {
	Headers []string  `yaml:"headers"`
	Time    TrackTime `yaml:"time"`
}

type TrackTime struct {
	Buckets []string `yaml:"buckets"`
}

type TrafficTarget struct {
	Name          string      `yaml:"name"`
	Method        string      `yaml:"method"`
	Protocol      string      `yaml:"protocol"`
	URL           string      `yaml:"url"`
	Replicas      int         `yaml:"replicas"`
	RequestCount  int         `yaml:"requestCount"`
	Expectation   Expectation `yaml:"expectation"`
	AutoInvoke    bool        `yaml:"autoInvoke"`
	StreamPayload []string    `yaml:"streamPayload,omitempty"`
	StreamDelay   string      `yaml:"streamDelay,omitempty"`
}

type Expectation struct {
	StatusCode    int               `yaml:"statusCode"`
	Payload       string            `yaml:"payload,omitempty"`
	PayloadLength int               `yaml:"payloadLength,omitempty"`
	Headers       map[string]string `yaml:"headers,omitempty"`
}
