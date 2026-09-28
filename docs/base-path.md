# Traefik 子路徑與 Gitea OAuth

設定 `DRONE_SERVER_BASE_PATH=/drone`，即可將本版本 Drone 掛在對外網址 `http://192.168.1.102:8083/drone`。留空或 `/` 維持原本根目錄行為，也可設定 `/tools/ci` 等多層路徑。路徑支援英數字、`-`、`_`，前後斜線會正規化；不要在這個欄位填完整 URL。

## Drone 設定

terminal 使用 tcsh。可從 repository 根目錄載入 [設定範本](../examples/drone-env.tcsh)：

```tcsh
source examples/drone-env.tcsh
# 使用本機 LSF mock 時再載入：
source examples/lsf/mock-env.tcsh
```

Gitea server 與 secret 可另存於本機 `.env.tcsh`，內容也使用 `setenv 名稱 值`，再用 `source .env.tcsh` 載入；此檔案已由 `.env.*` 規則排除於 Git。以下是逐項設定的寫法：

```tcsh
setenv DRONE_SERVER_PROTO http
setenv DRONE_SERVER_HOST 192.168.1.102:8083
setenv DRONE_SERVER_PORT :9987
setenv DRONE_SERVER_BASE_PATH /drone

# 使用既有的 Gitea server / OAuth credential 設定。
setenv DRONE_GITEA_SERVER 'http://你的-gitea-網址'
setenv DRONE_GITEA_CLIENT_ID 19ecb776-ea67-46a8-a635-0a3230299e39
# DRONE_GITEA_CLIENT_SECRET 請由現有環境或 secrets 設定提供。

# 未設定時會依照以上公開網址自動產生；若已有舊值，需更新。
setenv DRONE_GITEA_REDIRECT_URL http://192.168.1.102:8083/drone/login

# 延續 LSF runner 設定
setenv DRONE_AGENTS_DISABLED true
setenv DRONE_RUNNER_ENGINE lsf
```

`DRONE_SERVER_HOST` 只填對外 host 與 port，不包含 scheme 或 `/drone`。`DRONE_SERVER_PORT` 是 Drone 自己的監聽位址，與 Traefik 的對外 port 不同。LSF 其餘設定見 [LSF runner 文件](lsf-runner.md)。

Gitea 應用程式的 Redirect URI 必須是：

```text
http://192.168.1.102:8083/drone/login
```

新註冊／修復的 repository webhook 會使用 `http://192.168.1.102:8083/drone/hook`。既有 webhook 不會因 server 改設定而自動修改，請透過 repository 的 repair 操作更新，或在 Gitea 中核對 webhook URL。Gitea server 也需要能存取這個對外地址。若另設 `DRONE_SERVER_PROXY_HOST` / `DRONE_SERVER_PROXY_PROTO` 供 webhook 使用，它們也會附加相同 base path。

## Traefik 動態設定範例

以下採用 StripPrefix，假設既有 `web` entryPoint 監聽 `:8083`；entryPoint 名稱需對應你的 Traefik 設定。

```yaml
http:
  routers:
    drone:
      rule: "Host(`192.168.1.102`) && (Path(`/drone`) || PathPrefix(`/drone/`))"
      entryPoints:
        - web
      middlewares:
        - drone-strip-prefix
      service: drone

  middlewares:
    drone-strip-prefix:
      stripPrefix:
        prefixes:
          - /drone

  services:
    drone:
      loadBalancer:
        passHostHeader: true
        servers:
          - url: http://172.19.21.125:9987
```

Traefik 會把 `/drone/login` 轉送為 `/login`，並設定 `X-Forwarded-Prefix: /drone`，參考 [StripPrefix 官方文件](https://doc.traefik.io/traefik/reference/routing-configuration/http/middlewares/stripprefix/)。Drone 依照固定的 `DRONE_SERVER_BASE_PATH` 產生公開連結，不使用任意 forwarded header 決定公開 URL。也支援不使用 StripPrefix、直接把 `/drone/...` 原樣轉送給 Drone；後端會移除前綴再分派路由。

使用 StripPrefix 時請保留它設定的 `X-Forwarded-Prefix`，以區分已去掉前綴的路徑。例如 repository namespace 也叫 `drone` 時，外部 `/drone/drone/repo` 應只去掉一次 `/drone`。

## 涵蓋範圍與驗證

- OAuth 預設 callback、登入／登出跳轉、session 與 OAuth state cookie 路徑。
- 前端 Router basename、登入與登出連結、靜態 JS/CSS/media、manifest、API base URL 與 EventSource 串流路徑。
- repository webhook、SCM build status 連結、system public URL 與 `DRONE_BUILD_LINK`。
- API、RPC、health 與 metrics 可經相同 prefix 轉送。原始根路由仍可供內部工具存取；外部 runner 若不支援 URL path，可走內網原始 RPC endpoint。

本次使用模擬 OAuth provider 驗證 callback，並以本機 Chrome 驗證真實內嵌 UI 的 `/drone/welcome` 渲染與登入連結。沒有修改或部署現有 Traefik，也沒有連線使用你的 Gitea credentials 登入。

```tcsh
go test -race ./handler/basepath ./handler/web ./cmd/drone-server/config ./cmd/drone-server ./operator/runner
go build -tags 'oss nolimit' -o /tmp/drone-server-lsf ./cmd/drone-server
```

## 前端依賴維護

此 repository 的 UI 由 `github.com/drone/drone-ui v2.12.0+incompatible` 提供預先編譯資產，沒有本地前端建置流程。`handler/web/ui.go` 對這個固定版本的 application bundle 做有限的路徑適配，並調整 HTML 的 webpack public path；不修改第三方 bundle 或瀏覽器全域 API。

適配包含 React Router basename、API instance、以 pathname 比對 build 事件的地方，以及少數原生 anchor。每個替換點都檢查出現次數；升級 UI 若產物結構不符，會在啟動時明確報錯，必須同步更新適配器及測試。根路徑模式保留原始路徑，仍套用功能調整：移除 Promote 選單與部署表單中的 Promote 選項，並停用以 URL 開啟 Promote 表單；Debug 選單亦移除；Restart 改為套用 Cancel 樣式的直接操作按鈕，不再顯示下拉選單。執行中的 build 仍顯示 Cancel，已結束且具寫入權限時顯示 Restart。後端也不再註冊 Promote API。CSS／manifest 與調整後的主 bundle 在記憶體快取，不需要改寫 Go module cache 或提交產生的 JS。

適配後的主 JS 資源網址附加內容雜湊版本，避免瀏覽器以原始固定檔名的一年快取載入舊版按鈕或路徑設定。每次更新 bundle 適配內容後，重新整理網頁即可請求新版。

Settings → General 的管理員區塊另加入 Show LSF job information 開關，沿用既有表單狀態與 Save Changes 請求，並由 repository API 驗證管理員權限。

Timeout 改為整數小時輸入，預設 1 小時。Save Changes 先檢查非空白、正整數及 time.Duration 安全範圍，再以 `timeout_hours` 傳送；後端再次驗證並換算成分鐘儲存。修改沿用 Drone 管理員限制。舊有非整數小時設定仍保留並顯示實際換算值，管理員儲存前需改成整數小時。舊 API 的 `timeout` 仍以分鐘為單位，不能與 `timeout_hours` 同時傳入。

Repository 導覽列移除 Deployments，保留 Builds、Branches 與依權限顯示的 Settings；舊的 `/:namespace/:name/deployments` 網址導回該 repository 的 Builds。
