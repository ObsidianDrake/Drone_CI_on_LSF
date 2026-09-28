#!/bin/tcsh
# 從 repository 根目錄執行：source examples/drone-env.tcsh
# source 才能將設定載入目前 terminal；直接執行腳本不會改變父 shell。

setenv DRONE_SERVER_PROTO http
setenv DRONE_SERVER_HOST 172.19.21.125:8083
setenv DRONE_SERVER_PORT :9987
setenv DRONE_SERVER_BASE_PATH /drone
setenv DRONE_GITEA_REDIRECT_URL http://172.19.21.125:8083/drone/login
setenv DRONE_GITEA_CLIENT_ID 19ecb776-ea67-46a8-a635-0a3230299e39

# 使用既有 Gitea server 與 secret，或在本機私有的 .env.tcsh 中設定：
setenv DRONE_GITEA_SERVER http://172.19.21.125:8083/gitea
setenv DRONE_GITEA_CLIENT_SECRET gto_jjv3gqjpdg43cpls7get5dzqrbumcalmwbogryrqsleuewvnehxa
# source .env.tcsh

setenv DRONE_AGENTS_DISABLED true
setenv DRONE_RUNNER_ENGINE lsf

# 公司 Gitea 即使是公開 repository，也可能要求登入才能 clone。
setenv DRONE_GIT_ALWAYS_AUTH true

# 本機開發使用；正式 LSF 叢集請改為各節點可共同存取的共享路徑。
setenv DRONE_LSF_WORKSPACE /tmp/drone-lsf-work-$USER
setenv DRONE_LSF_QUEUE normal

# 若使用本機 mock，再執行：source examples/lsf/mock-env.tcsh
