# Alisten

在线音乐共享房间，支持实时同步播放、共享队列、多音源和管理后台。

本项目在 [bihua-university/alisten](https://github.com/bihua-university/alisten) 基础上重构。

## 自行部署

推荐容器化部署。

### 使用 Docker

供自己使用或本地验证：

```bash
cp .env.example .env
# 在 .env 中设置后台 token 和部署的版本号
docker compose up -d --wait
```

打开 <http://localhost:9876/admin>，输入刚设置的 `ALISTEN_ADMIN_TOKEN`，然后创建常驻房间并按需登录音源账号。

生产部署：

为应用和对象存储分别准备一个域名，并将其解析到部署服务器。随后在 `.env` 中填写生产环境配置，再执行：

```bash
docker compose \
  -f compose.yaml \
  -f compose.prod.yaml \
  up -d --wait
```

更新：

```bash
vim .env
# 将 ALISTEN_VERSION 修改为目标版本号
# 然后使用原来的部署命令重新启动
```

修改源代码后，自行构建和启动：

```bash
docker compose \
  -f compose.yaml \
  -f compose.local.yaml \
  up -d --build --wait
```

### 使用 Helm

Kubernetes 版本需为 1.30 或更高。该 Chart 仅包含 serve、worker 和 web，数据库、Valkey 和 S3 兼容对象存储需要单独部署。

参照 `helm/alisten/values.yaml` 配置域名、依赖服务地址和凭据，再执行：

```bash
# 命令中的 <release-version> 替换为真实的版本号
helm install alisten \
  oci://ghcr.io/dihemoe/alisten/charts/alisten \
  --version <release-version> \
  --namespace alisten \
  --create-namespace \
  -f values.yaml
```

更新：

```bash
# 命令中的 <release-version> 替换为真实的版本号
helm upgrade alisten \
  oci://ghcr.io/dihemoe/alisten/charts/alisten \
  --version <release-version> \
  --namespace alisten \
  -f values.yaml \
  --atomic \
  --wait \
  --timeout 6m
```

## 开放 API

常驻房间支持生成 API Key，供第三方应用（如 IM 平台机器人）调用本项目的开放接口。调用时须通过 `Authorization: Bearer <API Key>` 请求头进行身份鉴权。API Key 仅在生成或重置时返回一次，请妥善保管。

完整的接口定义、请求参数与响应结构见 [OpenAPI 规范文件](protocol/openapi.gen.json)。

| 接口 | 说明 |
| --- | --- |
| `GET /api/room/search/tracks` | 搜索曲目 |
| `GET /api/room/search/playlists` | 搜索歌单 |
| `GET /api/room/playlists/{source}/{playlistId}/tracks` | 获取歌单内的曲目 |
| `GET /api/room/recommendations` | 获取推荐曲目 |
| `POST /api/room/queue` | 按曲目或关键词点歌 |
| `DELETE /api/room/queue/{entryId}` | 删除等待队列中的指定曲目 |
| `POST /api/room/queue/{entryId}/likes` | 为等待队列中的指定曲目点赞 |
| `POST /api/room/skip-votes` | 投票跳过当前播放的曲目 |
| `PATCH /api/room/settings` | 设置播放模式 |
| `POST /api/room/messages` | 发送房间聊天消息 |

## 使用与版权

本项目不提供音源内容及其使用授权。音源名称、音频、封面和歌词等内容的权利归各自权利人所有。

请遵守音源平台的服务条款和适用法律，仅播放、下载或分享你有权使用的内容。

## 许可证

本项目采用 [MIT](LICENSE) 许可证。
