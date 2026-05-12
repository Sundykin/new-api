#!/usr/bin/env bash
# scripts/deploy.sh — 把本地代码以 amd64 docker 镜像发布到生产 new-api
#
# 用法：
#   ./scripts/deploy.sh                     # 构建 + 上传 + 强制重启 new-api
#   ./scripts/deploy.sh --size-limit 800    # 同时把 QINIU_MAX_UPLOAD_SIZE_MB 改为 800
#   ./scripts/deploy.sh --rebuild           # 忽略已有 tar，强制重新 buildx
#   ./scripts/deploy.sh --no-clean          # 部署完成后保留本地/远端 tar（默认会清掉）
#
# 前置：
#   - 本机 docker / buildx 可用
#   - ~/.ssh/config 里有 Host 别名（默认 newapi），可 ssh 直连服务器
#   - 服务端 compose 项目目录已就位（默认 /opt/new-api），所有 QINIU_* env 均通过
#     该目录下 docker-compose.yml 提供。本脚本不读写任何密钥。
#
# 可通过环境变量覆盖默认参数（见下方 ==== 可调参数 ====）。

set -euo pipefail

# ==== 可调参数 ====
SSH_HOST="${DEPLOY_SSH_HOST:-newapi}"
COMPOSE_DIR="${DEPLOY_COMPOSE_DIR:-/opt/new-api}"
SERVICE="${DEPLOY_SERVICE:-new-api}"
IMAGE_TAG="${DEPLOY_IMAGE_TAG:-komaapi/new-api:qiniu-upload}"
DOCKERFILE="${DEPLOY_DOCKERFILE:-Dockerfile.cn}"
PLATFORM="${DEPLOY_PLATFORM:-linux/amd64}"
LOCAL_TAR="${DEPLOY_LOCAL_TAR:-/tmp/new-api-qiniu-upload-amd64.tar}"
REMOTE_TAR="${DEPLOY_REMOTE_TAR:-/tmp/new-api-qiniu-upload-amd64.tar}"
HEALTH_PORT="${DEPLOY_HEALTH_PORT:-3000}"
HEALTH_TIMEOUT="${DEPLOY_HEALTH_TIMEOUT:-90}"

# ==== 参数解析 ====
SIZE_LIMIT=""
FORCE_REBUILD=0
CLEAN_TAR=1
while [ $# -gt 0 ]; do
  case "$1" in
    --size-limit) SIZE_LIMIT="$2"; shift 2 ;;
    --size-limit=*) SIZE_LIMIT="${1#*=}"; shift ;;
    --rebuild) FORCE_REBUILD=1; shift ;;
    --no-clean) CLEAN_TAR=0; shift ;;
    -h|--help) sed -n '2,20p' "$0"; exit 0 ;;
    *) echo "未知参数：$1（用 --help 查看用法）" >&2; exit 2 ;;
  esac
done

REPO_ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$REPO_ROOT"

step() { printf '\n\033[1;36m▶ %s\033[0m\n' "$*"; }
die()  { printf '\033[1;31m✗ %s\033[0m\n' "$*" >&2; exit 1; }

# ==== 0. 预检 ====
step "[0/5] 预检"
command -v docker >/dev/null || die "本机需要 docker"
docker buildx version >/dev/null 2>&1 || die "本机需要 docker buildx"
[ -f "$DOCKERFILE" ] || die "找不到 Dockerfile：$DOCKERFILE"
ssh -o ConnectTimeout=10 -o BatchMode=yes "$SSH_HOST" 'echo ok' >/dev/null \
  || die "无法 ssh $SSH_HOST，请检查 ~/.ssh/config"
echo "目标：$SSH_HOST → $COMPOSE_DIR ($SERVICE)，镜像 $IMAGE_TAG"

# ==== 1. 构建 amd64 镜像到 tar ====
step "[1/5] 构建 $PLATFORM 镜像 → $LOCAL_TAR"
if [ "$FORCE_REBUILD" = "1" ] || [ ! -s "$LOCAL_TAR" ]; then
  rm -f "$LOCAL_TAR"
  docker buildx build \
    --platform "$PLATFORM" \
    -f "$DOCKERFILE" \
    --tag "$IMAGE_TAG" \
    --output "type=docker,dest=$LOCAL_TAR" \
    .
else
  echo "已存在 $LOCAL_TAR（用 --rebuild 强制重建）"
fi
ls -lh "$LOCAL_TAR"

# ==== 2. scp 到服务器 ====
step "[2/5] scp 到 $SSH_HOST:$REMOTE_TAR"
scp -o ConnectTimeout=15 "$LOCAL_TAR" "$SSH_HOST:$REMOTE_TAR"

# ==== 3. docker load ====
step "[3/5] docker load on $SSH_HOST"
ssh "$SSH_HOST" "set -e
  docker load -i '$REMOTE_TAR'
  docker images '$IMAGE_TAG' --format 'loaded={{.ID}} created={{.CreatedSince}} size={{.Size}}'
"

# ==== 4. （可选）更新 QINIU_MAX_UPLOAD_SIZE_MB ====
if [ -n "$SIZE_LIMIT" ]; then
  step "[4/5] 更新 QINIU_MAX_UPLOAD_SIZE_MB=$SIZE_LIMIT（仅替换该行）"
  case "$SIZE_LIMIT" in *[!0-9]*) die "--size-limit 必须是正整数：$SIZE_LIMIT" ;; esac
  ssh "$SSH_HOST" "set -e
    cd '$COMPOSE_DIR'
    TS=\$(date +%Y%m%d_%H%M%S)
    cp docker-compose.yml docker-compose.yml.bak.\$TS
    echo \"备份：docker-compose.yml.bak.\$TS\"
    sed -i.tmp -E 's/^([[:space:]]*-[[:space:]]*QINIU_MAX_UPLOAD_SIZE_MB=)[0-9]+/\\1$SIZE_LIMIT/' docker-compose.yml
    rm -f docker-compose.yml.tmp
    echo '生效行：'
    grep -n 'QINIU_MAX_UPLOAD_SIZE_MB' docker-compose.yml || true
  "
else
  step "[4/5] 不调整 size limit（如需调整：--size-limit N）"
fi

# ==== 5. force-recreate + 健康检查 ====
step "[5/5] force-recreate $SERVICE + 健康检查"
ssh "$SSH_HOST" "set -e
  cd '$COMPOSE_DIR'
  docker compose up -d --force-recreate --no-deps '$SERVICE'
  echo '— 等待 healthcheck —'
  for i in \$(seq 1 $HEALTH_TIMEOUT); do
    S=\$(docker inspect '$SERVICE' --format '{{.State.Health.Status}}' 2>/dev/null || echo unknown)
    if [ \"\$S\" = healthy ]; then echo \"[\${i}s] healthy\"; break; fi
    if [ \"\$S\" = unhealthy ]; then echo \"[\${i}s] UNHEALTHY\"; exit 1; fi
    sleep 1
  done
  echo '— 容器状态 —'
  docker ps --filter \"name=^${SERVICE}\$\" --format 'table {{.Names}}\t{{.Image}}\t{{.Status}}'
  echo '— 当前 QINIU env —'
  docker inspect '$SERVICE' --format '{{range .Config.Env}}{{println .}}{{end}}' | grep '^QINIU_' | sort
  echo '— /api/status 探测 —'
  curl -s -m 5 \"http://localhost:$HEALTH_PORT/api/status\" | head -c 200 || true
  echo
"

# ==== 6. 清理（默认开启） ====
if [ "$CLEAN_TAR" = "1" ]; then
  step "[+] 清理临时 tar（--no-clean 可关闭）"
  rm -f "$LOCAL_TAR"
  ssh "$SSH_HOST" "rm -f '$REMOTE_TAR'"
fi

printf '\n\033[1;32m✅ 部署完成：%s\033[0m\n' "$IMAGE_TAG → $SSH_HOST:$COMPOSE_DIR"
echo "回滚（如需，恢复最近一次 size-limit 修改）："
echo "  ssh $SSH_HOST 'cd $COMPOSE_DIR && ls -t docker-compose.yml.bak.* | head -1 | xargs -I@ cp @ docker-compose.yml && docker compose up -d --force-recreate --no-deps $SERVICE'"
