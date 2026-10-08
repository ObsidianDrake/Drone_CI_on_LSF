# 本機 LSF 模擬環境

供 Drone LSF 整合開發使用，需 Linux 系統、Python 3.9+ 與 `tcsh`，不需 Python 套件或 LSF server。Drone engine 啟用方式見 [LSF runner 文件](../../docs/lsf-runner.md)。

在 repository 根目錄啟用（只影響目前 shell）：

```tcsh
setenv PATH "${cwd}/tools/lsf-mock/bin:${PATH}"
rehash
# 選用：指定獨立狀態目錄；預設 ~/.local/state/lsf-mock
setenv LSF_MOCK_STATE_DIR /tmp/lsf-mock-$USER
command -v bsub bkill bjobs tcsh
```

## 使用

```tcsh
# 背景提交；印出 Job <ID>，命令使用 tcsh 語法
bsub -J demo -q normal -oo '/tmp/demo-%J.log' 'set message = hello; echo $message; sleep 60'
bjobs
# 將 1 換成 bsub 回傳的 ID
bkill 1
bjobs -a
bjobs -json 1

# 等待完成，bsub 回傳工作的 exit code
bsub -K -oo /tmp/result.log 'echo $LSB_JOBID; hostname; exit 0'

# 也支援多個 argv，保留參數內的空白
bsub -K /usr/bin/printf '%s\n' 'hello world'

# 從 stdin 提交 tcsh script（腳本內的 #BSUB 指令不解析）
bsub -K < job.tcsh
```

## 支援範圍

- `bsub`：非同步提交；`-K` 等待完成；`-J`、`-q`、`-n`、`-R`、`-m` 記錄 metadata；`-cwd` 指定執行目錄。
- `-env all`（預設）繼承提交環境；`-env none` 清除提交環境，但仍提供執行帳號的 `HOME`、`USER`、工作目錄 `PWD` 與 LSF job 變數。測試可用 `LSF_MOCK_EXEC_HOME` 指定模擬執行節點的 HOME；此控制變數不會自動傳入 job。
- `-o` / `-e` 附加 stdout / stderr，`-oo` / `-eo` 覆寫；路徑支援 `%J` job ID，父目錄必須已存在，相對路徑以提交目錄為準。
- 預設 stdout / stderr 分別寫入狀態目錄的 `<ID>.out` / `<ID>.err`。只指定 stdout 時，stderr 仍寫入預設檔案。
- 每個工作使用 `tcsh -f`（不讀 `.tcshrc`），依 `-env` 選擇環境，依 `-cwd` 選擇目錄，並設定 `LSB_JOBID`、`LSB_JOBNAME`、`LSB_QUEUE`、`LSB_DJOB_NUMPROC`。Drone wrapper 會另外執行所選 shell 的初始化。
- 單一 command 參數視為 tcsh 表達式；多個 command 參數視為 argv。如需管線、重導向或多個命令，請將完整表達式包成單一參數。
- `bkill ID [ID ...]` 非同步提出取消要求；worker 約每 50ms 檢查，以 SIGKILL 終止 tcsh 與同一 session 的子程序（包含 tcsh 背景工作的程序群組），記錄 `EXIT` / 137。不使用持久化 PID 向其他程序發送訊號。
- `bjobs` 顯示未完成工作；`-a` 包含已完成工作；指定 ID 可查完成工作。`-json` 是本模擬器的診斷介面，並非完整 LSF JSON 格式。
- `bjobs -a -l ID` 顯示 job 名稱、使用者、queue、command、實際本機 host、cwd 與輸出檔案。
- 支援 engine 使用的 `bjobs -a -noheader -o 'stat exit_code' ID`。
- 狀態為 `PEND` → `RUN` → `DONE` / `EXIT`；job ID 分配有檔案鎖，支援同時提交。工作不依賴提交 shell 存活，狀態保存在本機 JSON。

這是開發用子集：沒有遠端執行、queue 排程、CPU/記憶體限制、job array、依賴條件、互動模式或 LSF mail/report。未支援的 CLI 選項會報錯。`-m` 不選擇執行主機，`-n`、`-R` 不限制資源；自行使用 `setsid` 脫離 session的程序不在取消範圍。worker 被外部強制終止或主機重啟後，不會自動恢復或校正狀態。每個使用者應使用自己的狀態目錄；有工作執行時不要刪除或切換該目錄。

真實 LSF 的預設 bkill 會依序使用多個訊號，本模擬器則直接強制終止，見 [IBM bkill 說明](https://www.ibm.com/docs/en/spectrum-lsf/10.1.0?topic=reference-bkill)。

## 驗證

```tcsh
python3 -m unittest discover -s tools/lsf-mock -p 'test_*.py' -v
```

測試使用臨時狀態目錄，涵蓋 tcsh 語法、環境、輸出、錯誤退出碼、參數空白、並行提交與取消子程序。

The workload trend collector also supports `bjobs -a -noheader -o 'jobid stat job_name:250' JOB_ID...`.
It returns one line per requested mock job, with the generated job name used to
verify that the tracked ID still belongs to the original Drone submission.
