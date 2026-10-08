package netrc

import (
	"strings"
	"testing"

	"github.com/drone/drone/core"
	"github.com/drone/drone/mock"
	"github.com/drone/go-scm/scm"
	"github.com/golang/mock/gomock"
)

func TestGiteaTokenAsPassword(t *testing.T) {
	controller := gomock.NewController(t)
	defer controller.Finish()
	user := &core.User{Login: "ci-user", Token: strings.Repeat("t", 194)}
	renew := mock.NewMockRenewer(controller)
	renew.EXPECT().Renew(gomock.Any(), user, true)
	s := Service{renewer: renew, client: &scm.Client{Driver: scm.DriverGitea}}
	got, err := s.Create(noContext, user, &core.Repository{Private: true, HTTPURL: "http://drc:8080/gitea/org/repo.git"})
	if err != nil {
		t.Fatal(err)
	}
	if got.Machine != "drc" || got.Login != user.Login || got.Password != user.Token {
		t.Fatal("incorrect Gitea clone credentials")
	}
}
