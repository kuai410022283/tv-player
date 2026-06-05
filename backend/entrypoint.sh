#!/bin/sh
set -e

# 如果容器以 root 用户启动，则自动修复挂载目录的权限，并降权运行
if [ "$(id -u)" = '0' ]; then
    # 确保数据目录与下载目录存在
    mkdir -p /app/data
    mkdir -p /app/web/download
    mkdir -p /app/library/channel_logo
    # 递归修改挂载目录的所有权给 mediaplayer 用户 (UID/GID 1000)
    chown -R mediaplayer:mediaplayer /app/data
    chown -R mediaplayer:mediaplayer /app/web/download
    chown -R mediaplayer:mediaplayer /app/library/channel_logo
    
    # 使用 su-exec 降权到 mediaplayer 用户并执行传入的命令
    exec su-exec mediaplayer "$@"
else
    # 如果已经以非 root 用户身份运行，则直接执行命令
    exec "$@"
fi
