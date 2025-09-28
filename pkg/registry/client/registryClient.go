package registryclient

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"goto/pkg/constants"
	"goto/pkg/global"
	"goto/pkg/transport"
	"goto/pkg/util"
	"io"
	"strings"
)

type RegistryClient struct {
	client      transport.ClientTransport
	baseURL     string
	basePeerURL string
}

type RegistryLockerClient struct {
	*RegistryClient
	context string
	locker  string
}

var (
	DefaultClient *RegistryClient
)

func InitRegistryClient() {
	DefaultClient = NewRegistryClient()
}

func NewRegistryClient() *RegistryClient {
	baseURL := fmt.Sprintf("%s/registry", global.Self.RegistryURL)
	basePeerURL := fmt.Sprintf("%s/peers/%s/%s", baseURL, global.Self.Name, global.Self.Address)
	return &RegistryClient{
		client:      transport.CreateDefaultHTTPClient("RegistryClient", false, false, nil),
		baseURL:     baseURL,
		basePeerURL: basePeerURL,
	}
}

func (rc *RegistryClient) OpenLocker(context, locker string) (*RegistryLockerClient, error) {
	if err := rc.checkRegistry(); err != nil {
		return nil, err
	}
	url := ""
	if context != "" {
		url = fmt.Sprintf("%s/context/%s/lockers/%s/open", rc.baseURL, context, locker)
	} else {
		url = fmt.Sprintf("%s/lockers/%s/open", rc.baseURL, locker)
	}
	err := rc.post(url)
	if err != nil {
		return nil, err
	}
	return &RegistryLockerClient{RegistryClient: rc, context: context, locker: locker}, nil
}

func (rc *RegistryLockerClient) Store(keys []string, data any) error {
	return rc.putDataInLocker(false, keys, data)
}

func (rc *RegistryLockerClient) StorePeer(keys []string, data any) error {
	return rc.putDataInLocker(true, keys, data)
}

func (rc *RegistryLockerClient) GetJSON(keys []string) (map[string]any, error) {
	return rc.getJSONFromLocker(false, keys)
}

func (rc *RegistryLockerClient) LoadJSON(keys []string, j any) error {
	b, err := rc.getDataFromLocker(false, keys)
	if err != nil {
		return err
	}
	err = json.Unmarshal(b, j)
	return err
}

func (rc *RegistryLockerClient) GetData(keys []string) ([]byte, error) {
	return rc.getDataFromLocker(false, keys)
}

func (rc *RegistryLockerClient) GetPeerJSON(keys []string) (map[string]any, error) {
	return rc.getJSONFromLocker(true, keys)
}

func (rc *RegistryLockerClient) GetPeerData(keys []string) ([]byte, error) {
	return rc.getDataFromLocker(true, keys)
}

func (rc *RegistryLockerClient) getJSONFromLocker(peer bool, keys []string) (map[string]any, error) {
	b, err := rc.getDataFromLocker(peer, keys)
	if err != nil {
		return nil, err
	}
	j := map[string]any{}
	json.Unmarshal(b, &j)
	return j, nil
}

func (rc *RegistryLockerClient) getDataFromLocker(peer bool, keys []string) ([]byte, error) {
	if err := rc.checkRegistry(); err != nil {
		return nil, err
	}
	url := ""
	path := strings.Join(keys, ",")
	if peer {
		url = fmt.Sprintf("%s/locker/get/%s", rc.basePeerURL, path)
	} else if rc.context != "" {
		url = fmt.Sprintf("%s/context/%s/lockers/%s/get/%s", rc.baseURL, rc.context, rc.locker, path)
	} else {
		url = fmt.Sprintf("%s/lockers/%s/get/%s", rc.baseURL, rc.locker, path)
	}
	return rc.get(url)
}

func (rc *RegistryLockerClient) putDataInLocker(peer bool, keys []string, data any) error {
	if err := rc.checkRegistry(); err != nil {
		return err
	}
	url := ""
	path := strings.Join(keys, ",")
	if peer {
		url = fmt.Sprintf("%s/locker/store/%s", rc.basePeerURL, path)
	} else if rc.context != "" {
		url = fmt.Sprintf("%s/context/%s/lockers/%s/store/%s", rc.baseURL, rc.context, rc.locker, path)
	} else {
		url = fmt.Sprintf("%s/lockers/%s/store/%s", rc.baseURL, rc.locker, path)
	}
	return rc.postWithInput(url, data)
}

func (rc *RegistryClient) checkRegistry() error {
	if !global.Flags.UseLocker || global.Self.RegistryURL == "" {
		return errors.New("Registry not configured or locker use disabled")
	}
	return nil
}

func (rc *RegistryClient) postWithInputAndOutput(url string, input any, callback func([]byte)) (err error) {
	var body io.Reader
	if input != nil {
		b, err := json.Marshal(input)
		if err != nil {
			return err
		}
		body = bytes.NewReader(b)
	} else {
		body = util.EmptyBody()
	}
	resp, err := rc.client.HTTP().Post(url, constants.ContentTypeJSON, body)
	if resp != nil {
		defer util.CloseResponse(resp)
	}
	if err != nil {
		return err
	}
	if resp.StatusCode >= 400 {
		return fmt.Errorf("Failed to post data to registry with status code: %d, status: %s. URL: %s", resp.StatusCode, resp.Status, url)
	}
	if callback != nil {
		b, err := io.ReadAll(resp.Body)
		if err != nil {
			return err
		}
		callback(b)
	}
	return nil
}

func (rc *RegistryClient) postWithInput(url string, input any) (err error) {
	return rc.postWithInputAndOutput(url, input, nil)
}

func (rc *RegistryClient) post(url string) (err error) {
	return rc.postWithInputAndOutput(url, nil, nil)
}

func (rc *RegistryClient) get(url string) (data []byte, err error) {
	resp, err := rc.client.HTTP().Get(url)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("Failed to get data from registry with status code: %d, status: %s. URL: %s", resp.StatusCode, resp.Status, url)
	}
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	return b, nil
}
