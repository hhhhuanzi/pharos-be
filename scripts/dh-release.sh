#!/usr/bin/env bash
#
# dh-release.sh —— Pharos(dh) 发布打包：FE 构建 → statik → 交叉编译 → tar
#
# 这条链之前靠人工执行，最容易漏的一步是「重新构建 FE 并重跑 statik」：pub/ 不进
# 版本库（.gitignore 有 /pub、/dist），front/statik/statik.go 虽然也写在 .gitignore
# 里，但它同时被 git 跟踪（与 upstream 一致），只 git pull 后直接 go build，拿到的
# 是上一次遗留的 statik 产物，二进制里是旧前端。
#
# 产物形态与 v1.1.0-pharos.2 对齐：目录 n9e-dh-<version>-linux-amd64，内含
# n9e / n9e-cli / n9e-edge / integrations/，不含 etc/（ops.yaml 由运维手工投放）。
#
# 官方 .goreleaser.yaml 面向 ccfos/nightingale 的 GitHub release（产物名
# n9e-v<version>-<os>-<arch>，archives.files 还含 etc/docker/agents 等），与本产物
# 形态不同，此处不复用、不修改它；官方 fe.sh / Makefile 同样不改动。

set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd -P)"

readonly DEFAULT_BRANCH='feature_link'
readonly TARGET_GOOS='linux'
readonly TARGET_GOARCH='amd64'
readonly VERSION_PKG='github.com/ccfos/nightingale/v6/pkg/version.Version'

VERSION=''
FE_DIR=''
OUT_DIR=''
BRANCH="${DEFAULT_BRANCH}"
EXPECT_BE_COMMIT=''
EXPECT_FE_COMMIT=''
ALLOW_DIRTY=0
DRY_RUN=0

# ---------------------------------------------------------------- 输出 --

step_no=0

step() {
  step_no=$((step_no + 1))
  printf '\n\033[1;36m[%d/%d] %s\033[0m\n' "${step_no}" "${TOTAL_STEPS}" "$*"
}

log() { printf '      %s\n' "$*"; }
warn() { printf '\033[1;33m  !!  %s\033[0m\n' "$*" >&2; }

die() {
  printf '\n\033[1;31m  xx  %s\033[0m\n' "$1" >&2
  shift
  local line
  for line in "$@"; do
    printf '      %s\n' "${line}" >&2
  done
  exit 1
}

usage() {
  cat <<'EOF'
用法: scripts/dh-release.sh <version> [选项]

  <version>                  发布版本号，如 v1.2.0-pharos.2

选项:
  --fe-dir <path>            前端仓库路径，默认 <be-repo>/../pharos-fe
  --out-dir <path>           产物输出目录，默认 <be-repo>/dist
  --branch <name>            期望分支名，默认 feature_link（两个仓库都校验）
  --expect-be-commit <sha>   断言后端 HEAD（支持短 sha 前缀）
  --expect-fe-commit <sha>   断言前端 HEAD（支持短 sha 前缀）
  --allow-dirty              允许工作区有未提交改动（仅供调试，产物名会带 -DIRTY）
  --dry-run                  只跑前置校验就退出，不构建
  -h, --help                 显示本帮助

示例:
  scripts/dh-release.sh v1.2.0-pharos.2 --expect-be-commit bc1dcafe
EOF
}

# ---------------------------------------------------------------- 参数 --

while [[ $# -gt 0 ]]; do
  case "$1" in
    -h | --help)
      usage
      exit 0
      ;;
    --fe-dir)
      FE_DIR="${2:?--fe-dir 缺少参数}"
      shift 2
      ;;
    --out-dir)
      OUT_DIR="${2:?--out-dir 缺少参数}"
      shift 2
      ;;
    --branch)
      BRANCH="${2:?--branch 缺少参数}"
      shift 2
      ;;
    --expect-be-commit)
      EXPECT_BE_COMMIT="${2:?--expect-be-commit 缺少参数}"
      shift 2
      ;;
    --expect-fe-commit)
      EXPECT_FE_COMMIT="${2:?--expect-fe-commit 缺少参数}"
      shift 2
      ;;
    --allow-dirty)
      ALLOW_DIRTY=1
      shift
      ;;
    --dry-run)
      DRY_RUN=1
      shift
      ;;
    -*)
      usage >&2
      die "未知选项: $1"
      ;;
    *)
      [[ -z "${VERSION}" ]] || {
        usage >&2
        die "多余的位置参数: $1"
      }
      VERSION="$1"
      shift
      ;;
  esac
done

[[ -n "${VERSION}" ]] || {
  usage >&2
  die '缺少版本号参数'
}

FE_DIR="${FE_DIR:-${REPO_ROOT}/../pharos-fe}"
OUT_DIR="${OUT_DIR:-${REPO_ROOT}/dist}"
[[ -d "${FE_DIR}" ]] || die "前端仓库不存在: ${FE_DIR}" '用 --fe-dir 指定正确路径'
FE_DIR="$(cd "${FE_DIR}" && pwd -P)"
mkdir -p "${OUT_DIR}"
OUT_DIR="$(cd "${OUT_DIR}" && pwd -P)"

if [[ "${DRY_RUN}" -eq 1 ]]; then
  readonly TOTAL_STEPS=1
else
  readonly TOTAL_STEPS=6
fi

# ---------------------------------------------------------- 前置校验 --

# 洁净度校验的豁免名单：被 git 跟踪、但每次构建都会被本脚本重写的生成物。
# front/statik/statik.go 写在 .gitignore 里，可它同时是被跟踪的文件（与 upstream 保持
# 一致，这里不改它的跟踪状态），而 gitignore 对已跟踪文件无效 —— 所以每打一次包它就
# 变脏，不豁免的话下一次打包会被上一次的构建产物挡下，防夹带闸门只能用一次。
# pub/ 和 dist/ 是真的未被跟踪（.gitignore 的 /pub、/dist），不会出现在 porcelain 里。
readonly BE_EXEMPT_PATHS='front/statik/statik.go'

# 从 git status --porcelain 输出里滤掉豁免路径。每行形如 'XY <path>'：前两个字符是
# 状态码（' M'、'M '、'??'、'A ' 等），第三个字符是分隔空格，路径从第 4 个字符起。
# 只做精确路径匹配：重命名行（'R  old -> new'）和被 git 加引号的特殊路径都匹配不上，
# 一律仍算脏 —— 宁可误拦，不可漏放。
filter_exempt_paths() {
  local exempt_list="$1" line path candidate skip
  while IFS= read -r line; do
    [[ -n "${line}" ]] || continue
    path="${line:3}"
    skip=0
    while IFS= read -r candidate; do
      [[ -n "${candidate}" ]] || continue
      if [[ "${path}" == "${candidate}" ]]; then
        skip=1
        break
      fi
    done <<<"${exempt_list}"
    [[ "${skip}" -eq 1 ]] || printf '%s\n' "${line}"
  done
}

check_repo() {
  local dir="$1" name="$2" expect_commit="$3" exempt="${4:-}"
  local actual_branch head_sha dirty kept

  git -C "${dir}" rev-parse --git-dir >/dev/null 2>&1 ||
    die "${name} 不是 git 仓库: ${dir}"

  actual_branch="$(git -C "${dir}" rev-parse --abbrev-ref HEAD)"
  head_sha="$(git -C "${dir}" rev-parse HEAD)"
  log "${name}: ${dir}"
  log "  分支 ${actual_branch}  HEAD ${head_sha}"

  [[ "${actual_branch}" == "${BRANCH}" ]] ||
    die "${name} 不在期望分支上：当前 ${actual_branch}，期望 ${BRANCH}" \
      "切分支：git -C ${dir} checkout ${BRANCH}"

  if [[ -n "${expect_commit}" && "${head_sha}" != "${expect_commit}"* ]]; then
    die "${name} HEAD 与期望不符：${head_sha}，期望以 ${expect_commit} 开头"
  fi

  # 防夹带核心防线。未被跟踪且 gitignore 掉的目录（pub/、dist/ 等）本就不会出现在
  # --porcelain 输出里；被跟踪的构建生成物按豁免名单逐条精确剔除；剩下的任何一行都
  # 视为真正的源码改动，直接拒绝构建。
  dirty="$(git -C "${dir}" status --porcelain)"
  if [[ -n "${exempt}" && -n "${dirty}" ]]; then
    kept="$(printf '%s\n' "${dirty}" | filter_exempt_paths "${exempt}")"
    if [[ "${kept}" != "${dirty}" ]]; then
      log "  已豁免构建生成物（本次构建会重新生成）：$(printf '%s' "${exempt}" | tr '\n' ' ')"
    fi
    dirty="${kept}"
  fi
  if [[ -n "${dirty}" ]]; then
    if [[ "${ALLOW_DIRTY}" -eq 1 ]]; then
      warn "${name} 工作区不干净，但 --allow-dirty 已开启 —— 产物不可用于正式发布"
      printf '%s\n' "${dirty}" >&2
    else
      printf '\n\033[1;31m  xx  %s 工作区有未提交改动，拒绝构建\033[0m\n' "${name}" >&2
      printf '%s\n' "${dirty}" >&2
      die '未提交的改动会被编译进发布产物（夹带）' \
        'aiagent/、pkg/dh/aibrand/ 属于 1.3.0，不能进 1.2.0 产物。' \
        "请先 stash：git -C ${dir} stash push -u -m 'wip 1.3.0'" \
        '或提交到 1.3.0 分支后再回到本分支重跑。' \
        '确需带着改动构建（非正式产物）时才加 --allow-dirty。'
    fi
  fi
}

require_cmd() {
  local cmd="$1" hint="$2"
  command -v "${cmd}" >/dev/null 2>&1 || die "缺少依赖: ${cmd}" "${hint}"
}

step '前置校验'
log "版本号: ${VERSION}"
log "目标平台: ${TARGET_GOOS}/${TARGET_GOARCH}（本机 $(uname -s | tr '[:upper:]' '[:lower:]')）"

check_repo "${REPO_ROOT}" 'BE(pharos-be)' "${EXPECT_BE_COMMIT}" "${BE_EXEMPT_PATHS}"
check_repo "${FE_DIR}" 'FE(pharos-fe)' "${EXPECT_FE_COMMIT}"

require_cmd node '安装 Node.js（建议与 CI 一致的 LTS 版本）'
require_cmd npm '随 Node.js 一起安装'
require_cmd go '安装 Go：https://go.dev/dl/'
require_cmd tar '系统自带'

GOPATH_BIN="$(go env GOPATH)/bin"
if command -v statik >/dev/null 2>&1; then
  STATIK_BIN="$(command -v statik)"
elif [[ -x "${GOPATH_BIN}/statik" ]]; then
  STATIK_BIN="${GOPATH_BIN}/statik"
else
  die '缺少依赖: statik' '安装：go install github.com/rakyll/statik@latest'
fi
log "node $(node --version)  npm $(npm --version)  $(go version)"
log "statik ${STATIK_BIN}"

BE_COMMIT="$(git -C "${REPO_ROOT}" rev-parse HEAD)"
FE_COMMIT="$(git -C "${FE_DIR}" rev-parse HEAD)"

PKG_NAME="n9e-dh-${VERSION}-${TARGET_GOOS}-${TARGET_GOARCH}"
if [[ "${ALLOW_DIRTY}" -eq 1 ]]; then
  PKG_NAME="${PKG_NAME}-DIRTY"
fi
STAGE_DIR="${OUT_DIR}/${PKG_NAME}"
TARBALL="${OUT_DIR}/${PKG_NAME}.tar.gz"
log "产物: ${TARBALL}"

if [[ "${DRY_RUN}" -eq 1 ]]; then
  printf '\n\033[1;32m  ok  前置校验通过（--dry-run，未构建）\033[0m\n'
  exit 0
fi

# ------------------------------------------------------------ FE 构建 --

step "构建前端（${FE_DIR}）"
if [[ ! -d "${FE_DIR}/node_modules" ]]; then
  log 'node_modules 缺失，执行 npm ci'
  (cd "${FE_DIR}" && npm ci)
fi
# vite 的 build.outDir 就是 'pub'（vite.config.ts:108），产物落在 FE 仓库根下的
# pub/，随后整份同步到 BE 供 statik 使用。
rm -rf "${FE_DIR}/pub"
(cd "${FE_DIR}" && npm run build)
[[ -f "${FE_DIR}/pub/index.html" ]] ||
  die "前端构建未产出 ${FE_DIR}/pub/index.html"
log "前端产物 $(du -sh "${FE_DIR}/pub" | cut -f1)"

step '同步前端产物到 BE 的 pub/'
# fe.sh 在没有 pub/ 时会去 GitHub 下载官方 FE release；这里用自己构建的产物直接覆盖，
# 绕开那段下载逻辑，同时保证 pub/ 一定是本次构建的结果。
rm -rf "${REPO_ROOT}/pub"
cp -R "${FE_DIR}/pub" "${REPO_ROOT}/pub"
[[ -f "${REPO_ROOT}/pub/index.html" ]] || die "同步失败：${REPO_ROOT}/pub/index.html 不存在"
log "$(find "${REPO_ROOT}/pub" -type f | wc -l | tr -d ' ') 个文件"

# -------------------------------------------------------------- statik --

step '生成 front/statik/statik.go'
# 等价于 fe.sh:24 的 statik -src=./pub -dest=./front，额外加：
#   -f 覆盖已存在的 statik.go（本地重复打包时必需，fe.sh 只在干净 CI 里跑）
#   -m 忽略 mtime，让同样的 pub/ 产出字节一致的 statik.go
rm -f "${REPO_ROOT}/front/statik/statik.go"
(cd "${REPO_ROOT}" && "${STATIK_BIN}" -src=./pub -dest=./front -f -m)
[[ -s "${REPO_ROOT}/front/statik/statik.go" ]] ||
  die 'statik 未生成 front/statik/statik.go'
log "statik.go $(du -h "${REPO_ROOT}/front/statik/statik.go" | cut -f1)"

# ------------------------------------------------------------ 交叉编译 --

step "交叉编译 ${TARGET_GOOS}/${TARGET_GOARCH} 三个二进制"

# 纯 Go 交叉编译的前提：构建图里不能出现需要 CGO 的包。生产代码用的是
# glebarez/sqlite（modernc，纯 Go，见 pkg/ormx/ormx.go:10），mattn/go-sqlite3 只被
# _test.go 引到，因此 CGO_ENABLED=0 成立（官方 .goreleaser.yaml 的 linux 构建同样
# 是 CGO_ENABLED=0）。这里做一次实测校验，避免以后有人引入 CGO 依赖后静默失败。
check_no_cgo_dep() {
  local pkg="$1" offenders
  offenders="$(
    cd "${REPO_ROOT}" &&
      CGO_ENABLED=0 GOOS="${TARGET_GOOS}" GOARCH="${TARGET_GOARCH}" \
        go list -deps "${pkg}" | grep -E 'mattn/go-sqlite3' || true
  )"
  [[ -z "${offenders}" ]] || die \
    "${pkg} 的构建图里出现了需要 CGO 的包，无法在 macOS 上纯 Go 交叉编译：" \
    "${offenders}" \
    '替代方案（任选其一）：' \
    '  1) 在 Linux amd64 机器上直接跑本脚本；' \
    '  2) 在 linux/amd64 容器里跑本脚本（镜像需自带 node + go + statik，' \
    '     把 BE 与 FE 两个仓库都挂进去，再用 --fe-dir 指到 FE 挂载点）；' \
    '  3) 把该依赖换成纯 Go 实现（如 glebarez/sqlite）。'
}

LDFLAGS="-w -s -X ${VERSION_PKG}=${VERSION}-${BE_COMMIT}"
log "ldflags: ${LDFLAGS}"

rm -rf "${STAGE_DIR}"
mkdir -p "${STAGE_DIR}"

build_one() {
  local main_pkg="$1" bin_name="$2"
  check_no_cgo_dep "${main_pkg}"
  log "go build ${main_pkg} -> ${bin_name}"
  (
    cd "${REPO_ROOT}" &&
      CGO_ENABLED=0 GOOS="${TARGET_GOOS}" GOARCH="${TARGET_GOARCH}" \
        go build -ldflags "${LDFLAGS}" -o "${STAGE_DIR}/${bin_name}" "${main_pkg}"
  )
  log "  $(du -h "${STAGE_DIR}/${bin_name}" | cut -f1)"
}

# main 包路径与 Makefile / .goreleaser.yaml 一致；用目录形式（cmd/edge 有两个文件）。
build_one ./cmd/center/ n9e
build_one ./cmd/cli/ n9e-cli
build_one ./cmd/edge/ n9e-edge

# ---------------------------------------------------------------- 打包 --

step '打包 tar.gz'
cp -R "${REPO_ROOT}/integrations" "${STAGE_DIR}/integrations"
# 刻意不放 etc/：与 v1.1.0-pharos.2 一致，ops.yaml 由运维手工投放（见 pharos-ops/UPGRADE.md）。

rm -f "${TARBALL}"

# macOS（Ventura 起）给本地构建的可执行文件打 com.apple.provenance 扩展属性，bsdtar
# 默认把 xattr 写成 pax 头（LIBARCHIVE.xattr.*），Linux 的 GNU tar 解包时不认识这些
# keyword，会逐文件打印 "Ignoring unknown extended header keyword"。--no-xattrs 是
# 真正的根治开关（实测单独加它就能让归档里的 LIBARCHIVE.xattr 归零）；
# --no-mac-metadata 管的是另一类东西 —— 不生成 ._* AppleDouble 条目，与
# COPYFILE_DISABLE=1 同义，留着做双保险。
# 逐个探测而不是写死：本脚本将来可能在 Linux 上跑，GNU tar 有 --no-xattrs 但没有
# --no-mac-metadata，写死会让整条 tar 命令因无法识别的参数直接失败。
TAR_OPTS=()
tar_probe_dir="$(mktemp -d)"
: >"${tar_probe_dir}/probe"
for opt in --no-xattrs --no-mac-metadata; do
  if tar -cf /dev/null "${opt}" -C "${tar_probe_dir}" probe 2>/dev/null; then
    TAR_OPTS+=("${opt}")
  else
    log "本机 tar 不支持 ${opt}，跳过"
  fi
done
rm -rf "${tar_probe_dir}"
log "tar 元数据参数: ${TAR_OPTS[*]:-（无）}"

# --exclude 与上面两个参数不重复：参数管的是 tar 自己合成的元数据，--exclude 管的是
# 磁盘上真实存在的垃圾文件（Finder 浏览 dist/ 留下的 .DS_Store、解压别处压缩包带进来
# 的 ._*），两者都要留。
COPYFILE_DISABLE=1 tar -czf "${TARBALL}" -C "${OUT_DIR}" \
  ${TAR_OPTS[@]+"${TAR_OPTS[@]}"} \
  --exclude '.DS_Store' --exclude '._*' "${PKG_NAME}"

actual_top="$(tar -tzf "${TARBALL}" |
  awk -F/ -v d="${PKG_NAME}" '$1 == d && $2 != "" { print $2 }' |
  LC_ALL=C sort -u)"
expected_top="$(printf '%s\n' integrations n9e n9e-cli n9e-edge | LC_ALL=C sort -u)"
if [[ "${actual_top}" != "${expected_top}" ]]; then
  die '压缩包顶层条目与上一版不一致' \
    "期望: $(echo "${expected_top}" | tr '\n' ' ')" \
    "实际: $(echo "${actual_top}" | tr '\n' ' ')"
fi

entry_count="$(tar -tzf "${TARBALL}" | wc -l | tr -d ' ')"
if command -v sha256sum >/dev/null 2>&1; then
  sha256="$(sha256sum "${TARBALL}" | awk '{print $1}')"
else
  sha256="$(shasum -a 256 "${TARBALL}" | awk '{print $1}')"
fi

printf '\n\033[1;32m  ok  打包完成\033[0m\n'
cat <<EOF

  产物      ${TARBALL}
  大小      $(du -h "${TARBALL}" | cut -f1)
  条目数    ${entry_count}（v1.1.0-pharos.2 为 1392，量级应接近）
  顶层      $(echo "${actual_top}" | tr '\n' ' ')
  sha256    ${sha256}
  BE commit ${BE_COMMIT}
  FE commit ${FE_COMMIT}

  提醒：包里没有 etc/，ops.yaml 需按 pharos-ops/UPGRADE.md 手工投放。
EOF
