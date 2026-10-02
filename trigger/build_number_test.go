package trigger

import (
	"context"
	"testing"

	"github.com/drone/drone/core"
	"github.com/drone/drone/mock"
	"github.com/drone/drone/store/build"
	"github.com/drone/drone/store/repos"
	"github.com/drone/drone/store/shared/db/dbtest"
	"github.com/golang/mock/gomock"
)

func TestTriggerUsesConfiguredNextBuildNumber(t *testing.T) {
	for _, badConfig := range []bool{false, true} {
		name := "pipeline"
		if badConfig {
			name = "configuration error"
		}
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			d, err := dbtest.Connect()
			if err != nil {
				t.Fatal(err)
			}
			defer d.Close()
			repository := repos.New(d)
			builds := build.New(d)
			repo := *dummyRepo
			repo.ID = 0
			repo.Counter = 0
			repo.Active = true
			repo.Trusted = true
			if err := repository.Create(ctx, &repo); err != nil {
				t.Fatal(err)
			}
			if err := repository.(core.RepositoryBuildNumberStore).UpdateNextBuildNumber(ctx, &repo, 5001); err != nil {
				t.Fatal(err)
			}
			ctrl := gomock.NewController(t)
			defer ctrl.Finish()
			users := mock.NewMockUserStore(ctrl)
			users.EXPECT().Find(gomock.Any(), repo.UserID).Return(dummyUser, nil).AnyTimes()
			raw := "kind: pipeline\ntype: lsf\nclone: {disable: true}\nsteps:\n- name: qc\n  commands: [echo test]\n"
			if badConfig {
				raw = "kind: [invalid yaml"
			}
			file := &core.Config{Data: raw}
			config := mock.NewMockConfigService(ctrl)
			config.EXPECT().Find(gomock.Any(), gomock.Any()).Return(file, nil)
			convert := mock.NewMockConvertService(ctrl)
			convert.EXPECT().Convert(gomock.Any(), gomock.Any()).Return(file, nil)
			validate := mock.NewMockValidateService(ctrl)
			validate.EXPECT().Validate(gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
			status := mock.NewMockStatusService(ctrl)
			status.EXPECT().Send(gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, _ *core.User, input *core.StatusInput) error {
				if input.Build.Number != 5001 {
					t.Fatalf("status used wrong number: %d", input.Build.Number)
				}
				return nil
			})
			queue := mock.NewMockScheduler(ctrl)
			if !badConfig {
				queue.EXPECT().Schedule(gomock.Any(), gomock.Any()).Return(nil)
			}
			hooks := mock.NewMockWebhookSender(ctrl)
			hooks.EXPECT().Send(gomock.Any(), gomock.Any()).Return(nil)
			service := New(nil, config, convert, nil, status, builds, queue, repository, users, validate, hooks)
			result, err := service.Trigger(ctx, &repo, dummyHook)
			if err != nil || result == nil || result.Number != 5001 {
				t.Fatalf("build=%+v error=%v", result, err)
			}
			stored, err := builds.FindNumber(ctx, repo.ID, 5001)
			if err != nil || stored.ID != result.ID {
				t.Fatalf("persisted build=%+v error=%v", stored, err)
			}
			if badConfig && stored.Status != core.StatusError {
				t.Fatalf("expected configuration error: %+v", stored)
			}
		})
	}
}
