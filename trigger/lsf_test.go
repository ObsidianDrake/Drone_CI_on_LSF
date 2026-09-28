package trigger

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/drone/drone/core"
	"github.com/drone/drone/mock"
	"github.com/golang/mock/gomock"
)

func TestCompanyYAMLDispatch(t *testing.T) {
	raw, err := os.ReadFile("../examples/lsf/company.drone.yml")
	if err != nil {
		t.Fatal(err)
	}
	for _, explicit := range []string{"", "docker", "lsf"} {
		t.Run(explicit, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			defer ctrl.Finish()
			source := string(raw)
			if explicit != "" {
				source = strings.Replace(source, "kind: pipeline", "kind: pipeline\ntype: "+explicit, 1)
			}
			file := &core.Config{Data: source}
			users := mock.NewMockUserStore(ctrl)
			users.EXPECT().Find(gomock.Any(), dummyRepo.UserID).Return(dummyUser, nil)
			repos := mock.NewMockRepositoryStore(ctrl)
			repos.EXPECT().Increment(gomock.Any(), dummyRepo).Return(dummyRepo, nil)
			config := mock.NewMockConfigService(ctrl)
			config.EXPECT().Find(gomock.Any(), gomock.Any()).Return(file, nil)
			convert := mock.NewMockConvertService(ctrl)
			convert.EXPECT().Convert(gomock.Any(), gomock.Any()).Return(file, nil)
			validate := mock.NewMockValidateService(ctrl)
			validate.EXPECT().Validate(gomock.Any(), gomock.Any()).Return(nil)
			status := mock.NewMockStatusService(ctrl)
			status.EXPECT().Send(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)
			queue := mock.NewMockScheduler(ctrl)
			queue.EXPECT().Schedule(gomock.Any(), gomock.Any()).Return(nil)
			builds := mock.NewMockBuildStore(ctrl)
			builds.EXPECT().Create(gomock.Any(), gomock.Any(), gomock.Any()).Do(func(_ context.Context, _ *core.Build, stages []*core.Stage) {
				want := "lsf"
				if explicit == "docker" {
					want = "docker"
				}
				if len(stages) != 1 || stages[0].Type != want {
					t.Fatalf("stages: %+v", stages)
				}
			}).Return(nil)
			hooks := mock.NewMockWebhookSender(ctrl)
			hooks.EXPECT().Send(gomock.Any(), gomock.Any()).Return(nil)
			service := New(nil, config, convert, nil, status, builds, queue, repos, users, validate, hooks)
			if _, err := service.Trigger(context.Background(), dummyRepo, dummyHook); err != nil {
				t.Fatal(err)
			}
		})
	}
}
