#!/bin/bash
# TVPlayer Admin 构建脚本
# 用法: ./web/build.sh [--minify]
#
# 无参数: 仅检查文件完整性
# --minify: 压缩 CSS/JS 到 dist/ 目录（需要 terser 和 cssnano）

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
cd "$SCRIPT_DIR"

echo "📦 TVPlayer Admin Build"
echo "======================="

# 检查文件完整性
echo ""
echo "📁 源文件:"
for f in index.html style.css app.js; do
  if [ -f "$f" ]; then
    size=$(wc -c < "$f" | tr -d ' ')
    lines=$(wc -l < "$f" | tr -d ' ')
    echo "  ✅ $f ($lines 行, $size bytes)"
  else
    echo "  ❌ $f 缺失!"
    exit 1
  fi
done

# 检查 HTML 引用
echo ""
echo "🔗 引用检查:"
if grep -q 'style.css' index.html; then
  echo "  ✅ index.html 引用 style.css"
else
  echo "  ❌ index.html 缺少 style.css 引用"
fi

if grep -q 'app.js' index.html; then
  echo "  ✅ index.html 引用 app.js"
else
  echo "  ❌ index.html 缺少 app.js 引用"
fi

# 统计
echo ""
echo "📊 统计:"
total_lines=$(cat index.html style.css app.js | wc -l | tr -d ' ')
total_bytes=$(cat index.html style.css app.js | wc -c | tr -d ' ')
echo "  总行数: $total_lines"
echo "  总大小: $total_bytes bytes ($(echo "scale=1; $total_bytes/1024" | bc) KB)"

# 可选压缩
if [ "${1:-}" = "--minify" ]; then
  echo ""
  echo "🔧 压缩模式..."

  mkdir -p dist

  # 检查工具
  if command -v terser &>/dev/null; then
    terser app.js -c -m -o dist/app.min.js
    echo "  ✅ app.js → dist/app.min.js ($(wc -c < dist/app.min.js | tr -d ' ') bytes)"
  else
    echo "  ⚠️  terser 未安装 (npm i -g terser)，复制原文件"
    cp app.js dist/app.min.js
  fi

  if command -v cssnano &>/dev/null; then
    cssnano style.css dist/style.min.css
    echo "  ✅ style.css → dist/style.min.css"
  else
    # 简单压缩：去除注释和多余空白
    sed 's|/\*.*\*/||g; /^[[:space:]]*$/d; s/[[:space:]]*$//' style.css > dist/style.min.css
    echo "  ✅ style.css → dist/style.min.css (简单压缩)"
  fi

  # 生成压缩版 HTML
  sed \
    -e 's|/admin/style.css|/admin/dist/style.min.css|' \
    -e 's|/admin/app.js|/admin/dist/app.min.js|' \
    index.html > dist/index.min.html
  echo "  ✅ index.html → dist/index.min.html"

  echo ""
  echo "📦 压缩结果:"
  orig=$(cat index.html style.css app.js | wc -c | tr -d ' ')
  mini=$(cat dist/index.min.html dist/style.min.css dist/app.min.js | wc -c | tr -d ' ')
  echo "  原始: $orig bytes"
  echo "  压缩: $mini bytes"
  echo "  节省: $(echo "scale=1; (1-$mini/$orig)*100" | bc)%"
fi

echo ""
echo "✅ 构建完成"
