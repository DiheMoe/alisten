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

## 使用与版权

本项目不提供音源内容及其使用授权。音源名称、音频、封面和歌词等内容的权利归各自权利人所有。

请遵守音源平台的服务条款和适用法律，仅播放、下载或分享你有权使用的内容。

## 许可证

本项目采用 [MIT](LICENSE) 许可证。
