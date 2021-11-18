/**
 * Copyright 2021 uk
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

package grpc

import (
  "errors"
  "fmt"
  "goto/pkg/util"
  "os"
  "path/filepath"
  "sort"

  "github.com/jhump/protoreflect/desc"
  "github.com/jhump/protoreflect/desc/protoparse"
  "github.com/jhump/protoreflect/grpcreflect"
)

type GRPCParser struct {
  fileSources        map[string]*FileSource
  serviceDescriptors map[string]*desc.ServiceDescriptor
}

type FileSource struct {
  descriptors map[string]*desc.FileDescriptor
}

type ServerSource struct {
  client *grpcreflect.Client
}

var (
  grpcParser = &GRPCParser{
    fileSources:        map[string]*FileSource{},
    serviceDescriptors: map[string]*desc.ServiceDescriptor{},
  }
  ProtosDir = filepath.FromSlash(util.GetCwd() + "/protos/")
)

func (g *GRPCParser) AddProto(name, path string, content []byte) error {
  if path != "" {
    path = filepath.FromSlash(ProtosDir + path)
    if _, err := os.Stat(path); os.IsNotExist(err) {
      if err = os.MkdirAll(path, os.ModePerm); err != nil {
        return fmt.Errorf("Failed to create director [%s] with error: %s", path, err.Error())
      }
    }
  } else {
    path = ProtosDir
  }
  filename := name + ".proto"
  if filepath, err := util.StoreFile(path, filename, content); err == nil {
    p := protoparse.Parser{
      ImportPaths:           []string{path},
      InferImportPaths:      true,
      IncludeSourceCodeInfo: true,
    }
    descriptors := map[string]*desc.FileDescriptor{}
    if fds, err := p.ParseFiles(filename); err == nil {
      for _, fd := range fds {
        addFile(fd, descriptors)
      }
      fs := &FileSource{descriptors: descriptors}
      g.fileSources[name] = fs
      for svc, sd := range fs.GetServices() {
        g.serviceDescriptors[svc] = sd
      }
    } else {
      return fmt.Errorf("Failed to parse proto file [%s] with error: %s", filepath, err.Error())
    }
  }
  return nil
}

func (g *GRPCParser) ClearProtos() {
  g.fileSources = map[string]*FileSource{}
  os.RemoveAll(ProtosDir)
}

func (g *GRPCParser) GetService(name string) *desc.ServiceDescriptor {
  return g.serviceDescriptors[name]
}

func (g *GRPCParser) ListServices(proto string) (services []string) {
  if fs := g.fileSources[proto]; fs != nil {
    for s := range fs.GetServices() {
      services = append(services, s)
    }
  }
  return
}

func (g *GRPCParser) ListMethods(proto, service string) map[string][]string {
  var sds []*desc.ServiceDescriptor
  if fs := g.fileSources[proto]; fs != nil {
    if service != "" {
      if s, err := fs.FindSymbol(service); err == nil {
        if sd, ok := s.(*desc.ServiceDescriptor); ok {
          sds = append(sds, sd)
        }
      }
    } else {
      for _, sd := range fs.GetServices() {
        sds = append(sds, sd)
      }
    }
  }
  serviceMethods := map[string][]string{}
  for _, sd := range sds {
    serviceName := sd.GetFullyQualifiedName()
    for _, method := range sd.GetMethods() {
      serviceMethods[serviceName] = append(serviceMethods[serviceName], method.GetFullyQualifiedName())
    }
    sort.Strings(serviceMethods[serviceName])
  }
  return serviceMethods
}

func addFile(fd *desc.FileDescriptor, fds map[string]*desc.FileDescriptor) {
  name := fd.GetName()
  if _, present := fds[name]; !present {
    fds[name] = fd
    for _, dep := range fd.GetDependencies() {
      addFile(dep, fds)
    }
  }
}

func (fs *FileSource) GetServices() (services map[string]*desc.ServiceDescriptor) {
  services = map[string]*desc.ServiceDescriptor{}
  for _, fd := range fs.descriptors {
    for _, svc := range fd.GetServices() {
      services[svc.GetFullyQualifiedName()] = svc
    }
  }
  return
}

func (fs *FileSource) FindSymbol(fullyQualifiedName string) (desc.Descriptor, error) {
  for _, fd := range fs.descriptors {
    if dsc := fd.FindSymbol(fullyQualifiedName); dsc != nil {
      return dsc, nil
    }
  }
  return nil, errors.New(fmt.Sprintf("Name [%s] not found in descriptors\n", fullyQualifiedName))
}

func (s *ServerSource) ListServices() ([]string, error) {
  svcs, err := s.client.ListServices()
  return svcs, reflectionSupport(err)
}

func (s *ServerSource) FindSymbol(fullyQualifiedName string) (desc.Descriptor, error) {
  file, err := s.client.FileContainingSymbol(fullyQualifiedName)
  if err != nil {
    return nil, reflectionSupport(err)
  }
  d := file.FindSymbol(fullyQualifiedName)
  if d == nil {
    return nil, errors.New(fmt.Sprintf("Symbol [%s] not found", fullyQualifiedName))
  }
  return d, nil
}
