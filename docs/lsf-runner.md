# Drone 的 LSF engine

內建 runner 可選擇 `docker`（預設）或 `lsf`。LSF 模式領取 `type: lsf` pipeline；省略 `type` 且所有 steps 的 `image` 都是 `git` 或 `none` 時，也會辨識為 LSF。每個 step 各提交一個 `bsub` job，預設使用執行節點的 `tcsh -f -e` 執行 `commands`。明確指定的 `type: docker` 保持 Docker 行為。保留 Drone 的條件判斷、step 相依性、secret、失敗處理、日誌與狀態回報。

## 本機模擬啟動

需要 Go 1.25.0 以上及 GCC；gRPC 已升級至 `v1.84.0`。SQLite driver
需要 `CGO_ENABLED=1`，不可透過關閉 CGO 解決 glibc 相容性問題。

開發時可直接執行 source（Go 仍會自動編譯，不必手動 build），沿用已設定的 tcsh 環境：

```tcsh
source examples/drone-env.tcsh
source examples/lsf/mock-env.tcsh
env CGO_ENABLED=1 go run -tags 'oss nolimit' ./cmd/drone-server
```

也可從 repository 根目錄，先編譯 server，再於保留原有 SCM、資料庫及 server 設定的環境中啟動：

```tcsh
env CGO_ENABLED=1 go build -tags 'oss nolimit' -o /tmp/drone-server-lsf ./cmd/drone-server

setenv PATH "${cwd}/tools/lsf-mock/bin:${PATH}"
rehash
setenv LSF_MOCK_STATE_DIR /tmp/drone-lsf-mock-$USER
setenv DRONE_AGENTS_DISABLED true
setenv DRONE_RUNNER_ENGINE lsf
setenv DRONE_LSF_WORKSPACE /tmp/drone-lsf-work-$USER
setenv DRONE_LSF_QUEUE normal

/tmp/drone-server-lsf
```

`DRONE_AGENTS_DISABLED=true` 啟用此 repository 的內建 runner；外部 runner 不受這個 engine 設定影響。同一個 server 的內建 runner 一次選擇一種 engine；Docker pipeline 仍可由外部 Docker runner 處理。

先將要執行的 repository 設為 **Trusted**。LSF 模式直接以 LSF 帳號執行主機命令，沒有容器隔離。server 需要 `bsub`、`bjobs`、`bkill`，執行節點需要 `/bin/sh`、`/usr/bin/env`、`tcsh`，自動 clone 另需 `git`。

將 [範例 pipeline](../examples/lsf/.drone.yml) 複製為目標 repository 的 `.drone.yml` 並觸發 build。預設 clone step 也會提交到 LSF；只想測試命令時，可設定 `clone: {disable: true}`。

```yaml
kind: pipeline
type: lsf
name: test
clone:
  disable: true
steps:
- name: run
  commands:
  - 'set greeting = hello'
  - 'echo "$$greeting, job=$$LSB_JOBID"'
```

Drone 在執行前會展開 YAML 中的環境變數；執行時才由 tcsh 展開的 `$` 必須寫成 `$$`。所有 commands 在同一個 tcsh script 中執行，可保留 `set`、`cd` 與 `source` 的效果；`-f` 不讀 `.tcshrc`，公司環境初始化可明確使用 `source /shared/company/setup.tcsh`。`-e` 在失敗時退出；commands 必須使用 tcsh 語法。

## RHEL 8 正式建置

公司主機使用 RHEL 8（glibc 2.28）。啟用 CGO 後，執行檔會依賴編譯環境的
glibc，因此必須在 RHEL 8 或 UBI 8 中編譯，不能直接搬用 Ubuntu 編譯的動態
連結執行檔。安裝 Go 1.25.0 以上、GCC、glibc-devel、binutils 及 file 後，
在 repository 根目錄執行（可從 tcsh 呼叫）：

```tcsh
bash scripts/build-rhel8.sh
env CGO_ENABLED=1 go test -mod=readonly ./store/repos ./operator/runner/lsf
```

產物為 `dist/drone-server`。腳本強制使用 `CGO_ENABLED=1` 及系統 GCC，並檢查
glibc 符號版本及執行檔能否載入；資料庫測試使用記憶體 SQLite。

GitHub Actions 也在 `registry.access.redhat.com/ubi8/ubi:8.10` 中執行相同建置
及 SQLite 測試；UBI 公開套件庫沒有 tcsh，因此 LSF 整合測試在安裝 tcsh 的
GitHub runner 上執行。通過後上傳 `drone-server-linux-amd64.tar.gz` 與 SHA-256 檔案。
這裡的容器只用於編譯；部署時仍直接在 RHEL 8 執行 server。
既有 `.drone.yml` 的 `scripts/build.sh` 則保留供 Alpine 映像使用的靜態連結建置。

公司環境沿用既有 Nexus / `GOPROXY` 設定。升級 gRPC 也會更新間接依賴；
若其他版本被 Nexus 回覆 403，需確認該套件的核准版本。

## 公司既有 YAML

[公司格式範例](../examples/lsf/company.drone.yml) 可直接作為 `.drone.yml`，不必補上 `type`：

```yaml
kind: pipeline
name: run_QC
environment: &env
  BSUB_OPTION: -q pdkd3_4_gitea_8.q
  SHELL_TYPE: csh
clone:
  disable: true
steps:
  - name: clone
    image: git
    environment: *env
  - name: step1
    image: none
    environment: *env
    commands:
      - echo "hello"
```

`image: git` 是原生 clone 標記，限用於名為 `clone`、沒有自訂 commands 的 step；會 fetch 並 checkout webhook 指定的 commit SHA。`clone.disable: true` 只停用自動 clone，仍執行明確列出的 clone step。`image: none` 在共享 workspace 執行 commands，不啟動容器。

頂層 environment 作為各 step 的預設值；step environment 可覆寫，支援 YAML anchor 與 `from_secret`。`BSUB_OPTION` 會依引號拆成參數直接傳給 bsub，例如：

```yaml
BSUB_OPTION: >-
  -q my_queue -m "host1 host2" -R 'select[os==RHEL8] rusage[mem=2048]' -n 4
```

資源條件名稱與值需依公司叢集設定。step 的 `-q`、`-R`、`-n` 分別取代 runner 的 queue、resources、slots 預設。選項解析不執行 shell substitution。工作名稱、工作目錄、輸出與環境由 Drone 管理，因此拒絕 `-J`、`-cwd`、`-o` / `-oo`、`-e` / `-eo`、`-env` 等衝突選項，也不接受互動模式或 `-K` 等同步提交模式。

`SHELL_TYPE` 支援 `csh`、`tcsh`、`sh`、`bash` 或這些 shell 的絕對路徑，執行節點必須已安裝。未指定時使用 `DRONE_LSF_SHELL`；commands 必須符合選定 shell 的語法。csh/tcsh 使用 `-f -e`，sh 使用 `-e`，bash 使用 `--noprofile --norc -e`，不自動讀取個人初始化檔案。

## 接公司 LSF

從 PATH 移除模擬工具，改用公司 LSF client；或設定各命令的絕對路徑。`DRONE_LSF_WORKSPACE` 必須是 server 與所有執行節點都以**相同絕對路徑**存取的共享檔案系統，且 LSF 帳號可讀寫。每條 pipeline 都建立獨立目錄，所有 steps 共用其中的 workspace。

| 設定 | 預設 | 用途 |
| --- | --- | --- |
| `DRONE_RUNNER_ENGINE` | `docker` | 設為 `lsf` 選用新 engine |
| `DRONE_LSF_BSUB` / `DRONE_LSF_BJOBS` / `DRONE_LSF_BKILL` | `bsub` / `bjobs` / `bkill` | CLI 路徑，也可指向公司 wrapper |
| `DRONE_LSF_WORKSPACE` | 系統暫存目錄下的 `drone-lsf` | 正式環境須設共享目錄 |
| `DRONE_LSF_QUEUE` | 空白 | 有設定才傳入 `bsub -q` |
| `DRONE_LSF_SLOTS` | `1` | `bsub -n` 預設值，step 可覆寫 |
| `DRONE_LSF_RESOURCES` | 空白 | `bsub -R`，完整字串作為單一參數 |
| `DRONE_LSF_SHELL` | `/bin/tcsh` | 預設 shell，step 可用 `SHELL_TYPE` 覆寫 |
| `DRONE_LSF_POLL_INTERVAL` | `1s` | 查詢狀態與讀取日誌間隔 |
| `DRONE_LSF_COMMAND_TIMEOUT` | `30s` | 單次 LSF CLI 的逾時 |
| `DRONE_LSF_CLEANUP_TIMEOUT` | `1m` | 取消後等待 LSF 確認結束的上限 |
| `DRONE_LSF_DEBUG_RETENTION` | `168h`（7 天） | Debug pipeline 確認收尾後的保留期限 |
| `DRONE_LSF_DEBUG_CLEANUP_INTERVAL` | `1h` | Debug 到期目錄的檢查間隔；啟動時也檢查 |
| `DRONE_RUNNER_CAPACITY` | `2` | 同時處理的 pipeline 數量，非 LSF job 總數上限 |

目前使用以下標準 CLI 行為，公司的 wrapper 也必須提供相容輸出：

- `bsub -J ... -cwd ... -oo ... -eo ... -n ... -env none [-q ...] [-R ...] /bin/sh <wrapper>`：解析回應中的 `Job <數字>`。
- `bjobs -a -noheader -o 'stat exit_code' <ID>`：`DONE` 對應成功，`EXIT` 回報退出碼；缺少有效退出碼時視為 `1`。等待、執行與暫停狀態持續輪詢。
- `bkill <ID>`：取消或 pipeline 逾時時呼叫，之後持續確認終止。查詢連續失敗三次也會進入取消收尾，避免失聯工作持續執行。

輸出直接寫入共享目錄並串流到 Drone；不依賴 LSF 最後才回傳的 spool log。LSF 自己的啟動診斷位於 step 目錄的 `scheduler.out` / `scheduler.err`。正常收尾會清除工作目錄。若 `bsub` 回應不明或無法確認 job 終止，會保留目錄並記錄錯誤，需依 server log 中的 job ID 或保留目錄名稱人工核對 LSF 狀態；server 重啟後尚無自動恢復 job 追蹤。

step 的 `HOME` 使用獨立目錄，clone 的 netrc 不會覆蓋服務帳號的 `~/.netrc`。環境只注入 pipeline / runner 的設定、Drone metadata、所需 secrets 與必要 LSF job 變數；沒有將 server 的完整環境直接交給命令。公司工具的 PATH 可透過 `DRONE_RUNNER_ENVIRON` 或 step environment 指定。

## 初版範圍

- 支援 `commands`、`environment`（含 `from_secret`）、`when`、`depends_on`、`failure: ignore`；`working_dir` 可指定 workspace 內已存在的相對子目錄。
- 自動 clone 使用 `git init`、fetch 並 checkout build commit SHA，支援 `clone.depth`、`clone.skip_verify` 與 Drone 提供的 HTTP netrc；尚未涵蓋 clone plugin 的 submodule、Git LFS 或 pull request 自動 merge 行為。
- `image` 僅支援省略、`none` 或 clone 專用的 `git`；不支援其他容器 image、容器 plugin settings、services、volumes、privileged、Docker network、container user、每個 step 的 Docker resources 或自訂 workspace 路徑。設定這些欄位會報錯，避免誤以為容器功能已生效。資源需求透過 `BSUB_OPTION` 或 runner 層級的預設設定提供。
- 沒有 LSF job arrays、互動工作或依賴 LSF 自己排 step 的功能；step 相依性由 Drone runtime 管理。
- 已以本機 mock 驗證，尚未連線公司 LSF 叢集驗證 wrapper、共享檔案系統與權限配置。

Graph View 使用 build API 中各 step 的 `depends_on` 繪製分支與匯合。內建 runner 會將編譯後的相依關係一併保存；升級前未保存相依資訊的舊 build 仍可能顯示直線，需重新執行 build 才會產生完整圖形資料。這項顯示修正不改變實際排程方式。

CLI 介面參考 IBM 的 [自訂 bjobs 輸出](https://www.ibm.com/docs/en/spectrum-lsf/10.1.0?topic=information-customize-job-output)、[bsub -env](https://www.ibm.com/docs/en/SSWRJV_10.1.0/lsf_command_ref/bsub.env.1.html) 與 [bsub -cwd](https://www.ibm.com/docs/en/spectrum-lsf/10.1.0?topic=options-cwd)。

## 驗證

```tcsh
go test -race ./operator/runner/lsf ./operator/runner ./trigger ./cmd/drone-server/config ./cmd/drone-server
python3 -m unittest discover -s tools/lsf-mock -p 'test_*.py' -v
```

Go 整合測試需要 tcsh，涵蓋公司 YAML 派送、webhook commit clone、各種 shell、BSUB_OPTION、共享 workspace、secrets 遮罩、即時日誌、fail-fast、退出碼、取消與 Drone stage 狀態、LSF 設定載入及 pipeline 類型領取。

每個 YAML commands 項目執行前，stdout 先以 ANSI 綠色顯示 `+ command`，再切為白色顯示執行結果；clone 命令亦同。命令顯示原始文字，不額外展開 shell 變數。多行 command 會整段顯示後執行，以保留 heredoc 與控制流程。執行程式自行輸出的 ANSI 色碼仍可改變顏色。執行節點須提供 `/usr/bin/printf`。

stderr 透過獨立 pipe 逐行以 ANSI 紅色串流到相同的 step log，行尾切回白色；stdout 保留原有輸出。無換行的 stderr 片段會在換行或關閉串流時顯示。執行節點須提供 `/usr/bin/mkfifo`。stdout 與 stderr 由不同串流處理，同時輸出時不保證跨串流的精確順序；程式若自行將 stderr 重導至 stdout，就無法再區分。LSF 的 `scheduler.out`／`scheduler.err` 不包含在此著色範圍。

原生 clone 的 Git 已知一般提示（`hint:`、`From ...`、fetch branch 狀態、`HEAD is now at ...` 與常見進度）會轉為白色 stdout 日誌。`fatal:`、`error:`、warning 與未辨識的 stderr 訊息保留紅色；其他 step 的 stderr 規則不變。這是依訊息格式分類，不影響 Git 的退出碼判定。

每個已確認結束的 LSF job（含成功、失敗與已確認取消）會在 step log 結尾直接附加該 step 的 `scheduler.out` 原始內容，保留原有欄位、縮排與換行，不再查詢或解析 `bjobs -a -l`。內容置於 `LSF job information (scheduler.out)` 區塊；不包含 `scheduler.err`。檔案不存在或無法讀取時顯示 `scheduler.out is unavailable.`，空檔顯示 `scheduler.out is empty.`，不改變 step 退出碼。讀取的是 job 確認結束時已可取得的檔案內容；未提交、跳過或尚無法確認終止的 job 不會附加此區塊。一般 build 仍在讀取後清理工作目錄，Debug build 則保留檔案。

## Repository 的 job information 開關

Drone 管理員可在 **Settings → General → Project Settings** 切換 **Show LSF job information**，再按 **Save Changes**。預設開啟，舊 repository 升級後也保留目前行為。

關閉後仍即時顯示綠色 command、白色 stdout 與紅色 stderr，工作狀態查詢及取消功能照常，只不附加結尾的 `scheduler.out` 內容。每個 LSF pipeline stage 開始執行時讀取 repository 設定；不會改變已執行中的 stage 或既有 log。

API 欄位為 `lsf_job_info_disabled`（`false` 表示顯示、`true` 表示隱藏）。非 Drone 管理員變更此欄位會收到 HTTP 403；其他設定儲存時帶入相同值不受影響。第一次啟動新版 server 時會自動新增資料庫欄位；附有 SQLite、MySQL 與 PostgreSQL migration，資料庫儲存測試以 SQLite 執行。


## Detached 背景服務

LSF step 支援 `detach: true`，完整可執行範例見 [detach.drone.yml](../examples/lsf/detach.drone.yml)。原生 `image: git` clone 不允許 detach。

- 取得 `bsub` 回傳的 job ID 並建立日誌串流後，即放行後續 steps，不等待該 job 結束。`depends_on: [service]` 在這裡表示等待服務提交；job 仍可能是 `PEND`，不保證已進入 `RUN` 或應用程式已就緒。
- 後續 step 應自行以具逾時的檢查等待 ready 檔、HTTP health check 或其他就緒條件。LSF jobs 可能位於不同 hosts；使用網路服務時須傳遞實際 host/port，不能假設 `localhost`。背景服務持續佔用 slot，queue 必須容納服務與消費者同時執行。
- pipeline 成功、失敗、取消或逾時收尾時，對尚未結束的 job 呼叫 `bkill`，並確認終止。仍在 `PEND` 的 job 也會取消。只有 detached steps 的 pipeline 會立即進入收尾，不會讓服務獨立存活。
- 背景服務自然結束的非零 exit code 不會單獨使 pipeline 失敗；其最終 step 狀態與 exit code 仍會記錄。提交失敗仍會使 pipeline 失敗。若服務失效應使測試失敗，消費者必須檢查服務是否可用。
- 正常收尾自動終止的 detached step 標為完成，保留 LSF 實際 exit code（例如 137）；取消／逾時終止則標為 killed。終態在 pipeline 收尾時更新，job 提前退出期間 step 可能仍顯示 running。
- 收尾會等待 detached 日誌排空及上傳，再完成 build；啟用「Show LSF job information」時也會包含 `scheduler.out` 區塊。若無法確認 job 終止，回報清理錯誤、保留 workdir，且不建立可自動到期刪除的完成紀錄。
- Debug 同樣會停止背景 job，只保留 workdir；保留期限沿用 Debug retention 設定。server 異常中斷仍需人工核對 LSF jobs，不提供跨重啟的自動恢復追蹤。

## 管理員 Debug 與工作目錄保留

已結束 build 的 Restart 旁提供 **Debug** 按鈕，只有 Drone 系統管理員可見；API 也會檢查系統管理員身分。Debug 會建立新的 build，並設定 `build.debug=true`，不會恢復已刪除的舊工作目錄。

LSF Debug build 正常執行所有 steps，成功、失敗或取消後保留整個 pipeline 目錄，包括 workspace、commands script、`scheduler.out` 與 `scheduler.err`。取消仍會呼叫 bkill 並確認終止；保留目錄不代表 job 繼續執行。每個已提交 step 的 Drone log 會列出 `[Debug]` 保留路徑，不受「Show LSF job information」開關影響。

一般 Restart 或 webhook build 維持原本清理行為；一般 Restart 不繼承前一次 build 的 Debug 標記。此功能適用於內建 LSF engine。

Debug 目錄預設在整個 pipeline 收尾、所有已提交的 LSF jobs 都確認結束後保留 **7 天**。server 啟動時及之後每小時掃描一次，清除到期目錄；實際刪除時間取決於下一次掃描，server 停機期間不會執行清理。一般 build 的清理方式不變。

每個符合清理條件的 Debug 目錄會以受限權限、原子寫入方式建立 `.drone-lsf-retention.json`，記錄版本、保留原因、目錄名稱、repository/build/stage ID、UTC 完成時間與到期時間。每條 pipeline（stage）分別起算，並非整個多 pipeline build 共用一個到期時間；不使用目錄 mtime 判斷。server 重啟後仍依檔案中的到期時間回收，修改設定只影響之後完成的 pipeline。

每個 step 的 Debug log 會顯示路徑、保留期限與 metadata 檔名。精確到期時間必須等整條 pipeline 收尾才知道，屆時寫入 metadata 與 server log。server log 也記錄刪除成功、失敗及需人工檢查的目錄。

執行中、無法確認 job 終止、`bsub` 提交結果不明、缺少或損壞完成紀錄（包括升級前的舊目錄）的工作目錄均不會自動刪除，需管理員核對 LSF 狀態後處理。server 異常中斷而未寫入完成紀錄的目錄也會保留。清理只檢查 workspace 直屬的 `pipeline-*` 目錄，不追蹤 pipeline 或 metadata 的符號連結；目錄內的符號連結只移除連結本身。清理失敗會記錄錯誤，保留完成紀錄以供後續重試。

可在啟動 server 前用 tcsh 設定，例如保留 3 天、每 30 分鐘檢查：

```tcsh
setenv DRONE_LSF_DEBUG_RETENTION 72h
setenv DRONE_LSF_DEBUG_CLEANUP_INTERVAL 30m
```

時間使用 Go duration 格式（例如 `168h`、`30m`，不支援 `7d`）；兩者需為正值，`0` 採用預設值，負值拒絕啟動。更新設定後需重啟 server。刪除 Drone build 歷史不會立即刪除保留的檔案。

目錄可能包含 secrets、clone 憑證與執行腳本，應維持原有受限權限，不要當作公開 artifact。LSF 的 `.out`／`.err` 仍可能是空檔，因為 commands 輸出由另一份 log 收集到 Drone。
