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

package protos

import (
	"context"
	"fmt"
	"goto/pkg/rpc/grpc"
	"goto/pkg/util"
	"os"
	"path/filepath"
	"sync"

	"github.com/bufbuild/protocompile"
	"github.com/bufbuild/protocompile/linker"
)

type ProtosStore struct {
	servicesByProto map[string][]*grpc.GRPCService
	fileSources     map[string]string
	lock            sync.RWMutex
}

var (
	ProtosRegistry = (&ProtosStore{lock: sync.RWMutex{}}).Init()
	protosDir      = filepath.FromSlash(util.GetCwd() + "/protos/")
)

func (ps *ProtosStore) Init() *ProtosStore {
	ps.lock.Lock()
	defer ps.lock.Unlock()
	ps.fileSources = map[string]string{}
	ps.servicesByProto = map[string][]*grpc.GRPCService{}
	grpc.ServiceRegistry.Init()
	return ps
}

func (ps *ProtosStore) parseProto(name, path string, content []byte) (linker.Files, error) {
	defer func() {
		if r := recover(); r != nil {
			fmt.Printf("Recovered from panic: %v\n", r)
		}
	}()
	if path != "" {
		path = filepath.FromSlash(protosDir + path)
		if _, err := os.Stat(path); os.IsNotExist(err) {
			if err = os.MkdirAll(path, os.ModePerm); err != nil {
				return nil, fmt.Errorf("failed to create director [%s] with error: %s", path, err.Error())
			}
		}
	} else {
		path = protosDir
	}
	filename := name + ".proto"
	if _, err := util.StoreFile(path, filename, content); err == nil {
		ps.lock.Lock()
		ps.fileSources[name] = filepath.FromSlash(path + "/" + filename)
		ps.lock.Unlock()
		compiler := protocompile.Compiler{Resolver: protocompile.WithStandardImports(&protocompile.SourceResolver{
			ImportPaths: []string{path}, // defaults to cwd if empty
		})}
		res, err := compiler.Compile(context.Background(), filename)
		if err != nil {
			return nil, err
		}
		return res, nil
	} else {
		return nil, err
	}
}

func (ps *ProtosStore) AddProto(name, path string, content []byte) error {
	files, err := ps.parseProto(name, path, content)
	if err != nil {
		return err
	}
	for _, file := range files {
		sds := file.Services()
		for i := 0; i < sds.Len(); i++ {
			sd := sds.Get(i)
			service := grpc.ServiceRegistry.NewGRPCService(sd)
			ps.lock.Lock()
			ps.servicesByProto[name] = append(ps.servicesByProto[name], service)
			ps.lock.Unlock()
		}
	}
	return nil
}

func (ps *ProtosStore) RemoveProto(proto string) {
	if services, ok := ps.servicesByProto[proto]; ok {
		for _, service := range services {
			grpc.ServiceRegistry.RemoveService(service.Name)
		}
		os.Remove(ps.fileSources[proto])
		ps.lock.Lock()
		defer ps.lock.Unlock()
		delete(ps.fileSources, proto)
		delete(ps.servicesByProto, proto)
	}
}

func (ps *ProtosStore) ClearProtos() {
	for proto := range ps.fileSources {
		ps.RemoveProto(proto)
	}
	ps.Init()
}

func (ps *ProtosStore) GetService(name string) *grpc.GRPCService {
	return grpc.ServiceRegistry.GetService(name)
}

func (ps *ProtosStore) ListServices(proto string) []*grpc.GRPCService {
	return ps.servicesByProto[proto]
}

func (ps *ProtosStore) ListMethods(service string) map[string]*grpc.GRPCServiceMethod {
	if s := grpc.ServiceRegistry.GetService(service); s != nil {
		return s.Methods
	}
	return nil
}
