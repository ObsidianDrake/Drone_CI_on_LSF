// Copyright 2019 Drone IO, Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//      http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package main

import (
	"fmt"

	"github.com/drone/drone-runtime/engine"
	"github.com/drone/drone-runtime/engine/docker"
	"github.com/drone/drone/cmd/drone-server/config"
	"github.com/drone/drone/core"
	"github.com/drone/drone/monitor"
	"github.com/drone/drone/operator/manager"
	"github.com/drone/drone/operator/runner"
	"github.com/drone/drone/operator/runner/lsf"

	"github.com/google/wire"
	"github.com/sirupsen/logrus"
)

// wire set for loading the server.
var runnerSet = wire.NewSet(
	provideRunner,
)

// provideRunner is a Wire provider function that returns a
// local build runner configured from the environment.
func provideRunner(
	manager manager.BuildManager,
	secrets core.SecretService,
	registry core.RegistryService,
	config config.Config,
	trends *monitor.Service,
) *runner.Runner {
	// the local runner is only created when remote agents
	// are disabled
	if config.Agent.Disabled == false {
		return nil
	}
	backend, err := provideEngine(config)
	if err != nil {
		logrus.WithError(err).
			Fatalln("cannot load the runner engine")
		return nil
	}
	if lsfEngine, ok := backend.(*lsf.Engine); ok {
		lsfEngine.TrackJob = trends.Track
	}
	return &runner.Runner{
		Type:       runnerType(config),
		Platform:   config.Runner.Platform,
		OS:         config.Runner.OS,
		Arch:       config.Runner.Arch,
		Kernel:     config.Runner.Kernel,
		Variant:    config.Runner.Variant,
		Engine:     backend,
		Manager:    manager,
		Secrets:    secrets,
		Registry:   registry,
		Volumes:    config.Runner.Volumes,
		Networks:   config.Runner.Networks,
		Devices:    config.Runner.Devices,
		Privileged: config.Runner.Privileged,
		Machine:    config.Runner.Machine,
		Labels:     config.Runner.Labels,
		Environ:    config.Runner.Environ,
		Limits: runner.Limits{
			MemSwapLimit: int64(config.Runner.Limits.MemSwapLimit),
			MemLimit:     int64(config.Runner.Limits.MemLimit),
			ShmSize:      int64(config.Runner.Limits.ShmSize),
			CPUQuota:     config.Runner.Limits.CPUQuota,
			CPUShares:    config.Runner.Limits.CPUShares,
			CPUSet:       config.Runner.Limits.CPUSet,
		},
	}
}

func runnerType(config config.Config) string {
	if config.Runner.Engine == "" {
		return "docker"
	}
	return config.Runner.Engine
}

func provideEngine(config config.Config) (engine.Engine, error) {
	switch runnerType(config) {
	case "docker":
		return docker.NewEnv()
	case "lsf":
		if len(config.Runner.Volumes)+len(config.Runner.Networks)+len(config.Runner.Devices)+len(config.Runner.Privileged) != 0 {
			return nil, fmt.Errorf("lsf: Docker runner volumes, networks, devices and privileged images are unsupported")
		}
		limits := config.Runner.Limits
		if limits.MemSwapLimit != 0 || limits.MemLimit != 0 || limits.ShmSize != 0 || limits.CPUQuota != 0 || limits.CPUShares != 0 || limits.CPUSet != "" {
			return nil, fmt.Errorf("lsf: use DRONE_LSF_SLOTS and DRONE_LSF_RESOURCES instead of Docker limits")
		}
		return lsf.New(lsf.Config{
			Bsub: config.LSF.Bsub, Bjobs: config.LSF.Bjobs, Bkill: config.LSF.Bkill,
			Workspace: config.LSF.Workspace, Queue: config.LSF.Queue,
			Resources: config.LSF.Resources, Shell: config.LSF.Shell, Slots: config.LSF.Slots,
			DisableShellInit: !config.LSF.ShellInit,
			PollInterval:     config.LSF.PollInterval, CommandTimeout: config.LSF.CommandTimeout,
			CleanupTimeout:       config.LSF.CleanupTimeout,
			DebugRetention:       config.LSF.DebugRetention,
			DebugCleanupInterval: config.LSF.DebugCleanupInterval,
		})
	default:
		return nil, fmt.Errorf("unsupported runner engine %q", config.Runner.Engine)
	}
}
