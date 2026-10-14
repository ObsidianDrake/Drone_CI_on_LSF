# Drone CI on LSF

以官方 Drone **v2.28.2** 為基礎，整合 IBM Spectrum LSF 的 CI server 衍生專案。此分支為 `Drone_LSF`，保留 Drone 的 repository、webhook、pipeline、secrets 與 build 管理流程，讓 pipeline steps 直接透過 `bsub` 在 LSF 執行節點上運行。

上游基準 commit：`477dc7709cf3d3a6471285e83ed33e712e35c7c9`。本專案不是獨立的外部 runner；LSF engine 整合在 Drone server 的內建 runner 中。

## 主要功能

- **原生 LSF pipeline**：每個 step 提交一個 LSF job，支援相依關係、平行 steps、取消、逾時與 detached 背景服務。
- **Shell 初始化**：預設使用 tcsh 並載入執行帳號的啟動檔；可依 step 選用 csh、tcsh、sh 或 bash。
- **Git clone**：支援 Git 2.8 以上，驗證實際 checkout 的 commit SHA；HTTP 認證使用專用 askpass，處理舊版 libcurl 的長 token 相容性。
- **即時日誌**：顯示 command、stdout、stderr，並可附加完整 `scheduler.out`。
- **管理員 Debug**：保留工作目錄與 shell 對應的環境快照，支援到期清理。
- **Workload monitor**：比較 Active repositories 的 build 與 LSF job 趨勢，提供七天歷史與高對比色盤。
- **管理介面**：build history 清理、空 repository 的下一次 build number 設定、以小時設定 timeout，以及更準確的手動 build 回饋。
- **Gitea 與子路徑部署**：OAuth 登入時同步 Gitea 網站管理員權限，可部署於 `/drone` 等 URL 子路徑。

## 執行架構與需求

Drone server 接收 webhook 或手動 build，解析 `.drone.yml`，由內建 LSF engine 提交 jobs，再透過 `bjobs` 追蹤狀態、串流日誌及以 `bkill` 取消工作。每條 pipeline 使用獨立目錄，其 steps 共用同一份 workspace。

| 位置 | 需求 |
| --- | --- |
| 建置主機 | Go 1.25.0 以上、GNU Make、GCC 與 libc 開發套件；使用 `CGO_ENABLED=1` |
| Drone server | 可存取 SCM、資料庫、LSF CLI 與共享 workspace；RHEL 8 部署應在 RHEL 8／UBI 8 建置 |
| LSF 執行節點 | `/bin/sh`、`/usr/bin/env`、所選 shell、Git 2.8 以上，以及 printf、mkfifo、awk 等系統工具 |
| 共享儲存 | server 與所有執行節點必須以**相同絕對路徑**讀寫 `DRONE_LSF_WORKSPACE` |
| Repository | 必須 Active，且由管理員設為 **Trusted** 才能執行原生 LSF pipeline |

目標執行節點包括 RHEL 6.7、7.7、8.10；Git 版本配置為 RHEL 6／7 使用 2.8.0，RHEL 8 使用 2.43.0。這是 **job 執行環境**，不代表 RHEL 8 建置的 server binary 可直接在 RHEL 6／7 啟動。Git 1.7.1 已不支援。

LSF steps 以服務帳號執行主機命令，沒有容器隔離。同一個 server 的內建 runner 一次選擇一種 engine；外部 Docker runner 可另外處理 Docker pipelines。

## 建置

在 repository 根目錄執行：

```sh
make
```

`make` 與 `make build` 相同，會建立 `bin/`，以 `CGO_ENABLED=1` 執行：

```sh
go build -o bin/drone-server ./cmd/drone-server
```

可直接從 tcsh／csh 呼叫 `make`。Makefile 不設定 `GOPROXY`、`GOPATH` 或公司工具路徑，沿用使用者的 Go 環境。Module cache 應放在不會被排程清除的目錄，避免套件下載或解壓內容被部分刪除。

### RHEL 8 部署產物

CGO binary 依賴建置主機的 libc。在 RHEL 8 或 UBI 8 中執行：

```sh
bash scripts/build-rhel8.sh
```

此腳本輸出 `dist/drone-server`，並檢查 glibc 2.28 相容性與動態載入。除 Go／GCC 外，需安裝 glibc 開發套件、binutils 及 `file`。一般 `make` 不包含這些部署相容性檢查。

目前 `go.mod` 使用 gRPC `v1.84.0` 與 protobuf `v1.36.12`；套件版本以 [go.mod](go.mod) 為準。原有建置說明另見 [BUILDING](BUILDING) 與 [BUILDING_OSS](BUILDING_OSS)。

## 啟動設定：Gitea + LSF

以下為 **tcsh 範本**，網址、路徑、queue 與 OAuth credentials 都必須替換成自己的設定。可存放於不提交 Git 的 `.env.tcsh`，再以 `source .env.tcsh` 載入；`.env.*` 已列入 `.gitignore`。

```tcsh
setenv DRONE_SERVER_PROTO https
setenv DRONE_SERVER_HOST ci.example.com
setenv DRONE_SERVER_PORT :8080

setenv DRONE_GITEA_SERVER https://git.example.com
setenv DRONE_GITEA_CLIENT_ID "REPLACE_WITH_CLIENT_ID"
setenv DRONE_GITEA_CLIENT_SECRET "REPLACE_WITH_CLIENT_SECRET"

setenv DRONE_DATABASE_DRIVER sqlite3
setenv DRONE_DATABASE_DATASOURCE /var/lib/drone/database.sqlite

setenv DRONE_AGENTS_DISABLED true
setenv DRONE_RUNNER_ENGINE lsf
setenv DRONE_RUNNER_CAPACITY 2
setenv DRONE_LSF_WORKSPACE /shared/drone/work
setenv DRONE_LSF_QUEUE normal
setenv DRONE_LSF_SHELL /bin/tcsh

# Load the site's LSF client environment before starting the server.
source /path/to/site/lsf.csh

./bin/drone-server
```

此範例假設由反向代理提供 HTTPS，並轉送到 Drone 的 `:8080`。資料庫父目錄與共享 workspace 必須先建立，且服務帳號需要適當權限。

- `DRONE_SERVER_HOST` 僅填對外 hostname 與可選 port，不含 scheme 或 URL path。
- Gitea OAuth Redirect URI 在此例為 `https://ci.example.com/login`；Gitea 必須能連到 Drone webhook URL。
- 此專案以 `DRONE_AGENTS_DISABLED=true` 啟用內建 runner，再由 `DRONE_RUNNER_ENGINE=lsf` 選擇 engine。
- 私有 repository 必須有有效的 SCM 存取權限；若 SCM 連公開 repository 都要求認證，可設定 `DRONE_GIT_ALWAYS_AUTH=true`。
- 子路徑部署可設定 `DRONE_SERVER_BASE_PATH=/drone`；OAuth callback 相應為 `/drone/login`。完整設定見 [子路徑部署](docs/base-path.md)。

### 常用 LSF 設定

| 環境變數 | 預設 | 用途 |
| --- | --- | --- |
| `DRONE_LSF_BSUB` / `DRONE_LSF_BJOBS` / `DRONE_LSF_BKILL` | `bsub` / `bjobs` / `bkill` | CLI 或相容 wrapper 的路徑 |
| `DRONE_LSF_WORKSPACE` | 系統暫存目錄下的 `drone-lsf` | 正式叢集應指定共享目錄 |
| `DRONE_LSF_QUEUE` | 未指定 | 預設 queue |
| `DRONE_LSF_SLOTS` | `1` | 預設 CPU slots |
| `DRONE_LSF_RESOURCES` | 未指定 | 預設 `bsub -R` 條件 |
| `DRONE_LSF_SHELL` | `/bin/tcsh` | 預設 command shell |
| `DRONE_LSF_SHELL_INIT` | `true` | 是否載入 shell 啟動檔 |
| `DRONE_RUNNER_CAPACITY` | `2` | 同時處理的 pipeline 數，不是 job 總數上限 |
| `DRONE_LSF_POLL_INTERVAL` | `1s` | LSF 狀態與日誌輪詢間隔 |
| `DRONE_LSF_COMMAND_TIMEOUT` | `30s` | 單次 CLI timeout |
| `DRONE_LSF_CLEANUP_TIMEOUT` | `1m` | 取消後等待終止確認的期限 |
| `DRONE_LSF_DEBUG_RETENTION` | `168h` | 已完成 Debug pipeline 的保留期限 |
| `DRONE_LSF_DEBUG_CLEANUP_INTERVAL` | `1h` | 到期目錄清理間隔 |

## `.drone.yml` 範例

下例明列 clone step，讓 clone 與一般 steps 都套用相同的 shell／queue 設定。`normal` 需替換為叢集內可使用的 queue。

```yaml
kind: pipeline
type: lsf
name: verification

clone:
  disable: true

environment:
  SHELL_TYPE: tcsh
  BSUB_OPTION: "-q normal -n 2"

steps:
  - name: clone
    image: git

  - name: verify
    image: none
    depends_on: [clone]
    commands:
      - 'echo "Build: $$DRONE_BUILD_NUMBER"'
      - 'git --version'
      - 'make'

trigger:
  event:
    - push
    - custom
```

- `image: git` 是原生 clone 標記，只能用於名為 `clone` 且沒有自訂 commands 的 step；不是容器 image。
- `clone.disable: true` 停用自動 clone，但仍執行上面明列的 clone step。未明列的自動 clone 使用 runner 預設或 runner 全域 environment。
- `image: none` 直接在 workspace 執行 commands；LSF engine 不支援任意 Docker images、container plugins 或 Docker volumes。
- 頂層 `environment` 提供明列 steps 的預設值，step environment 可以覆寫；支援 `from_secret`。
- Drone 會先展開 YAML 變數。要留給執行時的 shell 展開，請寫 `$$VARIABLE`。
- `+ NEW BUILD` 使用 `custom` event；branch、ref 與 event 必須符合 pipeline trigger。沒有符合的 pipeline 時，UI 會提示 **No build was created**。
- `BSUB_OPTION` 可設定 `-q`、`-n`、`-R` 等資源選項；Drone 自行管理 job name、cwd、輸出與環境，不接受衝突選項。

每個 step 的 LSF job name 格式為：

```text
Organization:Repository:BuildNumber:PipelineName:StepName
```

### Shell 與環境初始化

預設初始化為非互動、非 login 模式，不會開啟 terminal。`SHELL_INIT` 不必填；要略過啟動檔才設定 `SHELL_INIT: "false"`。

| Shell | 初始化方式 |
| --- | --- |
| tcsh | 載入原生 rc；優先 `~/.tcshrc`，不存在才讀 `~/.cshrc` |
| csh | 遵循節點上實際 shell 的原生 rc 規則 |
| bash | 明確 source `~/.bashrc` |
| sh | 明確載入 `~/.profile` |

初始化使用執行節點服務帳號的 HOME。選擇 sh 不會自動載入 `.cshrc`；commands 也必須符合所選 shell 的語法。Git 由初始化後的 PATH 選定。

LSF job 提交使用 `-env none`，不直接繼承 server 的一般環境；執行節點環境經 shell 初始化後，再套用 Drone 明確設定的環境。`DRONE_RUNNER_ENVIRON` 的同名值優先於 YAML，適合 runner 全域固定設定。

若 commands 需要再次呼叫 `bsub` 或其他 LSF 工具，必須確保 **step 執行環境** 已載入 LSF；server 可以提交 job 不代表 step 內也能找到 LSF commands。例如：

```yaml
commands:
  - 'source /path/to/site/lsf.csh > /dev/null'
  - 'bjobs'
```

tcsh／csh 每項 command 後會檢查退出碼；單一多行 command 以最後執行指令的狀態為準。`SHELL_OPTION`、初始化錯誤處理與完整限制見 [LSF runner 文件](docs/lsf-runner.md)。

## 管理與除錯

### Debug build

系統管理員可對已結束的 build 使用 **Debug**，建立新的 debug build。LSF jobs 照常執行與收尾，但保留 workspace、step scripts 與 scheduler logs。一般 Restart 不會沿用 Debug 標記。

每個實際啟動的 step 在第一項 command 前擷取環境，依 shell 產生 `env.csh`、`env.bash` 或 `env.sh`；log 會提供路徑與載入方式。以相同 shell 載入，例如：

```tcsh
source /path/to/step/env.csh
```

快照還原 exported environment 與工作目錄，不重跑 commands，也不保存 alias、function、未 export 的變數或 commands 執行後才改變的環境。Drone 管理的 secrets 與 clone 認證會排除；任意明文敏感值並非都能自動辨識，Debug 目錄仍應限制存取。

預設在 pipeline 所有 jobs 確認終止後保留七天，server 啟動時及每小時清理到期目錄。無法確認 job 終止或缺少有效完成紀錄的目錄不會自動刪除。刪除 build records 不會立即移除 Debug 目錄。

### Repository 設定與 history

- **Next Build Number**：僅系統管理員可修改，repository 必須 Active 且完全沒有任何 build records。允許調大、調小或重設為 `1`；儲存不會觸發 build，也不會改動既有 QC 目錄。
- **Timeout**：UI 使用正整數小時，預設一小時。
- **Show LSF job information**：控制 step log 結尾是否附加完整 `scheduler.out`，不影響即時輸出或 job 管理。
- **Build history cleanup**：系統管理員可預覽並刪除符合條件的已完成 records；清理本身不會降低 build counter。詳見 [Build history management](docs/build-history.md)。

Gitea 網站管理員身分在 OAuth 登入時同步到 Drone；repository admin 不等同系統管理員。同步規則與 `DRONE_USER_CREATE` 備援例外見 [Gitea 管理員同步](docs/gitea-admin-sync.md)。

### Workload monitor

從左側圖表圖示開啟 **Workload trends**，或使用 `/monitor`（有 base path 時例如 `/drone/monitor`）。

- 僅顯示目前 Active 且使用者有權存取的 repositories。
- 每 30 秒採樣與更新，保留七天；最多同時選擇 12 個 repos，預設選取六個。
- **Builds processing** 顯示 running builds；**LSF jobs processing** 顯示 RUN jobs。各自的 **Include queued** 可加入 pending／PEND。
- 使用高對比色盤，左側色點、曲線與提示資訊使用相同 repo 顏色；同時選取的 repos 不重複配色。
- LSF 圖表只計算內建 engine 登記的 jobs，不掃描叢集全部工作，也不將 CPU slots 當成 job 數量。

歷史從採樣功能啟動後累積；短於採樣間隔的 jobs 可能未被觀測，因此不是完整帳務或稽核紀錄。詳見 [Workload trends](docs/workload-trends.md)。

## 本機模擬與驗證

LSF mock 需要 Linux、Python 3.9+ 與 tcsh，不需真正的 LSF server。先設定自己的 Drone／SCM 環境，再從 repository 根目錄執行：

```tcsh
source examples/lsf/mock-env.tcsh
setenv DRONE_AGENTS_DISABLED true
setenv DRONE_RUNNER_ENGINE lsf
setenv DRONE_LSF_WORKSPACE /tmp/drone-lsf-work-$USER
make
./bin/drone-server
```

Mock 不提供真正的遠端排程或 CPU／記憶體限制。正式環境請使用真實 LSF CLI，不載入 mock PATH。詳細行為見 [LSF mock](tools/lsf-mock/README.md)。

主要驗證指令（tcsh／csh 亦可使用）：

```sh
env CGO_ENABLED=1 go test ./operator/runner/lsf ./operator/runner ./trigger ./cmd/drone-server/config ./cmd/drone-server ./handler/web ./handler/api/monitor ./monitor ./store/repos
python3 -m unittest discover -s tools/lsf-mock -p 'test_*.py' -v
```

Shell／mock 整合測試需安裝 tcsh、Python 與相關 Git 工具；前端互動測試需要 Node.js。RHEL binary 與 Git/libcurl 相容性驗證流程另見 [GitHub Actions workflows](.github/workflows)。

## 程式碼導覽

| 路徑 | 內容 |
| --- | --- |
| `cmd/drone-server/` | Server 啟動、設定與依賴組裝 |
| `operator/runner/lsf/` | LSF compiler、job 生命週期、clone、shell 初始化與 Debug |
| `trigger/` | 事件觸發與 pipeline 選擇 |
| `handler/web/` | 內嵌 UI 適配與客製介面 |
| `handler/api/` | Repository、build 與 monitor API |
| `monitor/` | Workload 採樣與 LSF tracking |
| `store/` | 資料庫存取與 migrations |
| `examples/lsf/` | Pipeline 與 mock 設定範例 |
| `scripts/` | 建置與相容性驗證工具 |

UI 基於固定版本的預編譯 `drone-ui` 資產，由 Go 端套用適配；此 repository 不需另跑 npm build。更新 UI 適配後需重新建置並啟動 server。更多細節見 [LSF runner](docs/lsf-runner.md) 與 [detached pipeline 範例](examples/lsf/detach.drone.yml)。

## 授權

保留上游的 [LICENSE](LICENSE) 與 [NOTICE](NOTICE)。不同 edition 與來源檔案適用的授權請依這些文件及檔頭確認；Community Edition 的建置方式見 [BUILDING_OSS](BUILDING_OSS)。目前 `make` 不額外加入 `oss nolimit` build tags。
