# Drone LSF 專案改善檢核報告

日期：2026-09-29  
狀態：待逐項核對，尚未核定實作範圍

## 目的與範圍

本報告整理目前 Drone LSF 分支的設計缺口與改善方向，作為逐項討論與決定實作順序的依據。建立本報告不代表已授權或完成以下改動。

分析依據是目前 workspace 的程式與文件，未進行正式 LSF 叢集的故障注入、負載測試或完整安全稽核。文中「現況」描述已閱讀的實作；「可能影響」是依該設計推論的情境，不表示已在正式環境發生。

目前已知需求：

- 使用 Gitea 與 Traefik `/drone` base path。
- 以原生 shell 與 LSF 執行 QC，單個工作可能持續 3～5 天。
- 透過 `BSUB_OPTION` 指定 LSF 資源與 queue。
- 不啟用自動取消舊 build。
- 監控以趨勢為主、保留七天，只追蹤 Drone 提交的 jobs。
- 預計使用模板動態產生 steps 與 dependencies。

## 核對總表

以下優先級是建議，尚待確認。P1 為優先處理，P2 為下一階段，P3 為中期維護改善；不是漏洞嚴重度評分。

| 核對 | ID | 議題 | 建議優先級 | 預估複雜度 | 決議 |
|---|---|---|---|---|---|
| [ ] | R01 | Server 重啟與 LSF 工作接管 | P1 | 高 | 待討論 |
| [ ] | R02 | LSF 查詢失敗與提交結果不明 | P1 | 中～高 | 待討論 |
| [ ] | R03 | 長時間 QC 的 timeout 定義 | P1 | 中 | 待討論 |
| [ ] | R04 | Jobs 併發限制與查詢負載 | P2 | 中～高 | 待討論 |
| [ ] | R05 | 執行帳號隔離與殘留 workspace | P2 | 中～高，涉及 infra | 待討論 |
| [ ] | R06 | Gitea 管理員撤權時效 | P2 | 中 | 待討論 |
| [ ] | R07 | 動態模板版本與執行快照 | P2；模板正式使用前 | 中～高 | 待討論 |
| [ ] | R08 | 監控異常辨識與擴充性 | P2 | 中 | 待討論 |
| [ ] | R09 | 大量歷史刪除與稽核 | P3 | 中 | 待討論 |
| [ ] | R10 | 前端維護、整合測試與部署驗證 | P3；部署驗證可先做 | 中～高 | 待討論 |

## R01 — Server 重啟與 LSF 工作接管

### 現況與依據

- LSF engine 的 pipeline／step 執行狀態主要保存在記憶體。
- 監控會持久化 job ID、名稱與 repository ID，重啟後可以恢復追蹤。
- 恢復監控不等於能恢復原本 build 的執行、log 收集或後續 dependencies。
- 依據：[engine.go](../operator/runner/lsf/engine.go)、[monitor/service.go](../monitor/service.go)、[監控說明](workload-trends.md)。

### 可能影響

Server 非預期退出時，LSF job 可能仍在執行；Drone 與 LSF 狀態可能不一致。正常關機與非預期退出的行為需要分別驗證，不能假設重啟後會續跑。

### 改善方向

持久化 build、stage、step、job ID、workspace、提交識別與 log 讀取位置。啟動後核對 LSF 狀態，重新接管可恢復的工作，避免重複提交。增加 drain 模式，停止接新工作並等待現有工作完成。

### 待核對

- [ ] 計畫性重啟時，是否要求現有 QC 不被取消？
- [ ] 非預期退出後，是否必須接管原 job 並繼續後續 steps？
- [ ] Drain 最長允許等待多久？超時後由誰決定後續動作？

### 建議驗收

- 執行中重啟後，job 不重複提交，log 能接續，依賴 step 不重複執行。
- 停機期間已完成的 job，恢復後能正確回報結果。
- 無法判定狀態時，有明確標記與管理處理入口。

決議／備註：待填。

## R02 — LSF 查詢失敗與提交結果不明

### 現況與依據

- Runner 的 `bjobs` 查詢連續失敗三次後，會進入 `bkill` 清理流程。
- `bsub` 未回傳可解析 job ID 時會保留 workspace，供核對；尚缺自動核對與接管流程。
- 依據：[engine.go](../operator/runner/lsf/engine.go) 的 `Start`、`monitor`、`Destroy`。

### 可能影響

短暫查詢異常可能取消正常執行的 QC。提交回應遺失則可能形成「工作已提交，但 Drone 不知道 job ID」；直接重試提交可能重複執行。

### 改善方向

區分使用者取消、timeout、查詢暫時失敗與提交結果不明。查詢故障使用退避重試與可設定的容忍期間；提交前持久化唯一識別，供事後核對。沒有足夠證據時不直接宣告成功，也不盲目重送。

### 待核對

- [ ] 查詢失敗可以容忍多久？
- [ ] 容忍期間屆滿後，要繼續等待、通知管理員，還是取消？
- [ ] 公司 LSF 有哪些可用的 job 名稱查詢與歷史查詢能力？

### 建議驗收

- 短暫查詢故障不會立即取消健康工作。
- 模擬提交成功但回應遺失，能核對原 job，且不重複提交。
- 使用者明確取消仍有效，並確認 LSF 終止後才清除執行資料。

決議／備註：待填。

## R03 — 長時間 QC 的 timeout 定義

### 現況與依據

Runner 使用 repository timeout 作為 stage 的時間預算，LSF `PEND` 等待也會消耗預算。UI 預設為一小時，與 3～5 天 QC 需要的設定不同。

依據：[runner.go](../operator/runner/runner.go)、[reaper.go](../service/canceler/reaper/reaper.go)。

### 改善方向

明確區分 Drone 排隊、LSF 排隊、實際執行與整體存活時間。先確認產品語意，再同步調整 runner、reaper 與 UI；這與自動取消舊 build 是不同機制。

### 待核對

- [ ] Repository timeout 要包含 LSF 排隊嗎？
- [ ] 上限要套用整個 pipeline，還是每個 step？
- [ ] 多個 steps 平行執行時，如何定義執行時間？
- [ ] 預設仍為一小時，或改由不同 QC 模板設定？

### 建議驗收

長時間 `PEND`、連續多 step、平行 step 與 server 重啟，都依相同且文件化的 timeout 規則處理；UI 能說明剩餘預算。

決議／備註：待填。

## R04 — Jobs 併發限制與查詢負載

### 現況與依據

- Runner capacity 與 repository／pipeline 限制主要控制 pipeline／stage 數量。
- 一個 pipeline 可有多個平行 steps，每個 step 都能提交 bsub。
- Runner 預設每個 job 每秒查詢一次；趨勢收集器另有批次查詢。
- 依據：[排程器](../scheduler/queue/queue.go)、[runner.go](../operator/runner/runner.go)、[engine.go](../operator/runner/lsf/engine.go)、[設定](../cmd/drone-server/config/config.go)。

### 可能影響

動態模板展開大量 steps 時，即使 build 併發不高，仍可能快速提交大量 jobs，增加 LSF 排隊與查詢負載。

### 改善方向

增加 pipeline、repository 與全域 jobs 配額；定義公平排程與模板 steps 上限。評估共用批次狀態查詢與適當的輪詢頻率。

### 待核對

- [ ] 配額要計算 `RUN`，還是所有已提交且未結束的 jobs？
- [ ] 預期最大 repository、build、job 同時執行數量各是多少？
- [ ] 是否需要不同團隊／queue 的配額與公平性？

### 建議驗收

大量平行 steps 不會突破配額；取消、失敗與重啟後配額可正確釋放；單一 repository 不會無限占用提交機會。

決議／備註：待填。

## R05 — 執行帳號隔離與殘留 workspace

### 現況與依據

- Native LSF 執行要求 trusted repository。
- 每個 pipeline 有獨立 workspace，step 使用獨立 HOME，憑證檔案有限制權限。
- 同一 Unix 帳號底下的 jobs，沒有容器式帳號隔離。
- 無法確認終止時保留 workspace，其中可能包含憑證或注入的環境資料。
- 依據：[compiler.go](../operator/runner/lsf/compiler.go)、[engine.go](../operator/runner/lsf/engine.go)、[LSF 說明](lsf-runner.md)。

### 改善方向

定義 trusted repository 與 PR 來源的信任政策；按需求分離執行帳號。建立殘留目錄列表、容量告警與確認終止後的清理流程，不可只依目錄年齡刪除仍在使用的資料。

### 待核對

- [ ] 所有 repository 是否可以共用一個 LSF 帳號？
- [ ] Fork／外部來源 PR 是否允許執行原生命令？
- [ ] 工作目錄、log 與失敗診斷資料要保留多久？
- [ ] 哪些資料必須清除，哪些結果必須另外保存？

### 建議驗收

清理不影響執行中的 jobs；憑證不被打包為可下載產物；執行帳號與資料存取範圍符合核定政策。

決議／備註：待填。

## R06 — Gitea 管理員撤權時效

### 現況與依據

目前登入時會同步 Gitea 管理員的授權與撤權；不是即時或背景持續同步。另有明確設定的本地管理員例外。

依據：[login.go](../handler/web/login.go)、[user.go](../service/user/user.go)、[同步說明](gitea-admin-sync.md)。

### 可能影響

Gitea 移除管理員身分後，現有 Drone 登入狀態不一定立即降權。先前「完整同步」的描述應精確理解為登入時雙向同步。

### 改善方向

對管理操作增加有期限的身分重新驗證，或定期同步並撤銷相關權限；明確定義 Gitea 不可用時的處理與本地管理員例外。

### 待核對

- [ ] 可以接受的撤權延遲是多少？
- [ ] Gitea 不可用時，管理操作是否應暫停？
- [ ] 是否保留本地緊急管理員？

### 建議驗收

既有 session 與 API token 在核定期限內失去管理能力；一般使用者功能不被不必要地中斷。

決議／備註：待填。

## R07 — 動態模板版本與執行快照

### 現況與依據

Organization Templates 依名稱讀取目前內容，支援 YAML、Starlark 與 Jsonnet。現有模板模型未提供不可變版本；Restart 會重新觸發設定取得與轉換流程。

依據：[template.go](../core/template.go)、[模板轉換器](../plugin/converter/template.go)、[retry.go](../handler/api/repos/builds/retry.go)、[trigger.go](../trigger/trigger.go)。

### 可能影響

同一 commit 在模板更新前後執行，可能產生不同 flow，影響問題重現與結果追溯。

### 改善方向

保存模板版本／雜湊、輸入參數及展開後的 pipeline 設定，排除實際 secret 值。提供展開預覽、dependency graph 驗證，以及明確的重跑語意。評估 steps 數量與模板執行資源上限。

### 待核對

- [ ] 模板要放在 Organization UI，或交由 Git 管理版本？
- [ ] Restart 預設重跑原始設定，還是使用最新模板？
- [ ] 是否需要兩種重跑選項？
- [ ] 模板版本與快照要保留多久？

### 建議驗收

能查出某次 build 實際使用的模板與展開結果；修改模板不會改寫舊 build 的快照；缺少 dependency、循環依賴或超量 steps 在提交 LSF 前被拒絕。

決議／備註：待填。

## R08 — 監控異常辨識與擴充性

### 現況與依據

- 趨勢每 30 秒取樣，保留七天；缺資料顯示斷線，短 jobs 可能不被取樣。
- 私有 repository 權限在每次請求中逐一向 SCM 查詢。
- 未取得狀態的 tracked job 會使相關 LSF 數值無效，缺少明確的歷史查詢退場程序。
- 目前支援單 server 與內建 LSF runner。
- 依據：[監控 API](../handler/api/monitor/trends.go)、[monitor/service.go](../monitor/service.go)、[監控說明](workload-trends.md)。

### 改善方向

維持原有趨勢需求，增加缺口原因、收集器健康狀態與遺失 job 核對；改善權限查詢效率並保留撤權時效。只有在確定需要多實例時，再加入選主與 runner 回報協定。

### 待核對

- [ ] 預期同時開啟監控頁面的使用者數量？
- [ ] 是否需要缺口原因與狀態告警？
- [ ] 近期是否需要外部 runner 或多 server？

### 建議驗收

查詢異常不被誤畫成零；已撤權 repository 不洩漏名稱或數量；大量 repositories 下刷新可在合理時間完成。

決議／備註：待填。

## R09 — 大量歷史刪除與稽核

### 現況與依據

目前提供管理員預覽與確認，保護未結束工作。All pages 清理會在同一 SQL transaction 中逐 build 處理；外部 log 清理失敗會記錄錯誤並回報失敗數量，尚缺持久化重試。

依據：[history.go](../store/build/history.go)、[刪除 API](../handler/api/repos/builds/history.go)、[功能說明](build-history.md)。

### 可能影響

資料量增加後，可能出現長交易、鎖等待、HTTP timeout 或外部 log 殘留。

### 改善方向

大量清理改為可追蹤進度的背景工作，保留預覽範圍與執行時保護條件。增加外部物件清理重試，以及獨立於 build 歷史的刪除稽核紀錄。

### 待核對

- [ ] 預計單一 repository 最多保留幾筆歷史？
- [ ] 是否需要自動保留政策，或維持手動清理？
- [ ] 刪除操作需要記錄哪些欄位，保存多久？

### 建議驗收

大量清理不中斷正常 build 寫入；失敗可重試且不超出已核定範圍；可追查誰在何時刪除了哪些紀錄。

決議／備註：待填。

## R10 — 前端維護、整合測試與部署驗證

### 現況與依據

- UI 透過對固定版本預編譯 bundle 的檢查與替換實作客製功能。
- 有 Go 與 JavaScript 測試，但這不足以取代瀏覽器實際排版、路由與互動測試。
- 目前 binary workflow 已使用 UBI 8 建置，並加入 SQLite 相關與 tcsh LSF 測試。
- 趨勢文件記載 MySQL／PostgreSQL migration 尚未在本 workspace 的實際資料庫伺服器上完成整合驗證。
- 依據：[ui.go](../handler/web/ui.go)、[CI workflow](../.github/workflows/ci-build-drone-lsf.yml)、[監控說明](workload-trends.md)。

### 改善方向

中期建立可獨立建置的前端原始碼。短期先加入 base path、登入、權限、Restart、Settings、歷史刪除與監控排版的端到端測試。部署驗證涵蓋正式 LSF 版本、共享檔案系統、資料庫備份還原與升級復原。

### 待核對

- [ ] 正式使用 SQLite、MySQL 或 PostgreSQL？
- [ ] 正式 LSF 版本與執行主機 OS 範圍？
- [ ] 可以接受的維護停機時間與資料復原目標？
- [ ] 前端是否預計持續大量客製化？

### 建議驗收

在正式環境相同條件下完成安裝、升級、還原演練；browser 測試可辨識 hover 版面位移與 `/drone` 路由回歸；發行物可追查 commit、Go 版本與建置環境。

決議／備註：待填。

## 建議討論順序

1. R01～R03：先決定長時間工作的存活、復原與 timeout 規則。
2. R04～R06：確認負載規模、執行信任邊界與管理員撤權時效。
3. R07：在啟用動態模板前，定義版本、預覽與重跑語意。
4. R08～R10：依實際規模完善監控、資料維護與部署測試。

每項決議可標記為「採納／部分採納／延後／不適用」，並記錄核定範圍、前置条件與驗收標準。完成核對後再拆分實作工作，避免把建議直接當成已確認需求。
