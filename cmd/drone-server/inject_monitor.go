package main

import (
	"github.com/drone/drone/cmd/drone-server/config"
	"github.com/drone/drone/monitor"
	"github.com/drone/drone/store/shared/db"
)

func provideMonitor(database *db.DB, config config.Config) *monitor.Service {
	return &monitor.Service{Store: &monitor.Store{DB: database}, Bjobs: config.LSF.Bjobs, Enabled: runnerType(config) == "lsf" && config.Agent.Disabled, CommandTimeout: config.LSF.CommandTimeout}
}
