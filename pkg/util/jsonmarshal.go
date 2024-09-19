/**
 * Copyright 2024 uk
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
  "encoding/json"
  "errors"
  "time"
)

type Duration struct {
  time.Duration
}

type JSONValueMarshal struct {
  Value interface{}
}

type JSONArrayMarshal struct {
  Values []interface{}
}

type JSONMapMarshal struct {
  Values map[string]interface{}
}

func (j *JSONArrayMarshal) MarshalJSON() ([]byte, error) {
  var data []interface{}
  for _, v := range j.Values {
    switch vv := v.(type) {
    case *JSONValue:
      data = append(data, &JSONValueMarshal{Value: vv.Value()})
    case JSONValue:
      data = append(data, &JSONValueMarshal{Value: vv.Value()})
    case []interface{}:
      data = append(data, &JSONArrayMarshal{Values: vv})
    case map[string]interface{}:
      data = append(data, vv)
    default:
      data = append(data, vv)
    }
  }
  return json.Marshal(data)
}

func (j *JSONMapMarshal) MarshalJSON() ([]byte, error) {
  data := map[string]interface{}{}
  for k, v := range j.Values {
    switch vv := v.(type) {
    case *JSONValue:
      data[k] = &JSONValueMarshal{Value: vv.Value()}
    case JSONValue:
      data[k] = &JSONValueMarshal{Value: vv.Value()}
    case []interface{}:
      data[k] = &JSONArrayMarshal{Values: vv}
    case map[string]interface{}:
      data[k] = vv
    default:
      data[k] = vv
    }
  }
  return json.Marshal(data)
}

func (j *JSONValueMarshal) MarshalJSON() ([]byte, error) {
  switch v := j.Value.(type) {
  case *JSONValue:
    return json.Marshal(&JSONValueMarshal{Value: v.Value()})
  case JSONValue:
    return json.Marshal(&JSONValueMarshal{Value: v.Value()})
  case []interface{}:
    return json.Marshal(&JSONArrayMarshal{Values: v})
  case map[string]interface{}:
    return json.Marshal(&JSONMapMarshal{Values: v})
  default:
    return json.Marshal(v)
  }
}

func (j *JSONValue) MarshalJSON() ([]byte, error) {
  return json.Marshal(&JSONValueMarshal{Value: j.Value()})
}

func (d Duration) MarshalJSON() ([]byte, error) {
  return json.Marshal(d.String())
}

func (d *Duration) UnmarshalJSON(b []byte) error {
  var v interface{}
  if err := json.Unmarshal(b, &v); err != nil {
    return err
  }
  switch value := v.(type) {
  case float64:
    d.Duration = time.Duration(value)
    return nil
  case string:
    var err error
    d.Duration, err = time.ParseDuration(value)
    if err != nil {
      return err
    }
    return nil
  default:
    return errors.New("Invalid duration")
  }
}
