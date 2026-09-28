# Gitea 管理員完整同步

Gitea 使用者每次完成 Drone OAuth 登入時，server 會以該使用者的 token 讀取 `GET <Gitea base URL>/api/v1/user`，將網站層級的 `is_admin` 同步至 Drone 的 `Admin`：

- `true`：授予 Drone 管理員權限，包含新帳號首次登入。
- `false`：撤銷既有 Drone 管理員權限。
- Gitea API 失敗、缺少 `is_admin` 或回應無效：拒絕本次登入，不猜測權限。
- 同步帳號無法寫入資料庫：拒絕建立新登入 session。

只適用 Gitea；不使用 organization owner 或 repository admin 權限判斷。其他 SCM 的管理員機制不變。更新 server 後自動啟用，無需額外環境變數。

## 本機備援管理員例外

保留 `DRONE_USER_CREATE` 中 `admin:true` 且非 machine 的指定帳號，登入時不因 Gitea `is_admin:false` 被降權，例如：

```tcsh
setenv DRONE_USER_CREATE "username:your_gitea_login,admin:true"
```

這是權限同步的明確例外，不是另一種登入方式；備援帳號仍需成功完成 Gitea OAuth 登入與有效的本人 API 查詢。帳號名稱比對不區分大小寫。

若要求所有帳號完全服從 Gitea，從啟動腳本移除這個設定，並在啟動 Drone 前執行：

```tcsh
unsetenv DRONE_USER_CREATE
```

移除備援設定不會立即改動資料庫；使用者下一次登入才依 Gitea 狀態同步。

## 套用與限制

```tcsh
source examples/drone-env.tcsh
# 使用 mock 開發環境才載入下一行
source examples/lsf/mock-env.tcsh
go run -tags 'oss nolimit' ./cmd/drone-server
```

重新啟動後，登出再登入 Drone。這是登入時同步，沒有背景輪詢 Gitea，也不會在 Gitea 權限變更當下主動撤銷既有 Drone session 或 API token。登入 API 查詢失敗時，既有登入狀態與資料庫權限不會因此被自動清除。

若公司 Gitea 限制 OAuth scopes，需保留能讀取本人使用者資料的權限；不要為此刪除 clone、repository 或 webhook 所需的既有 scopes。參考 [Gitea OAuth2 scope 說明](https://docs.gitea.com/development/oauth2-provider/)。

測試涵蓋管理員授予與撤銷、新帳號、備援例外、Gitea 子路徑與 Bearer token、API 欄位缺失／失敗及資料庫寫入失敗。尚未對公司 Gitea 做實際登入驗證。
