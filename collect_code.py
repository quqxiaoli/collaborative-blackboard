# -*- coding: utf-8 -*-
import os
import pathlib

# 功能：收集指定目录下的所有 Go 源代码到一个文件中
# 使用：在项目根目录下运行此脚本 (需要安装 Python)

OUTPUT_FILE = "project_code_dump.txt"
PROJECT_ROOT = "." # 假设脚本在项目根目录运行

# 需要扫描的目录列表 (相对于项目根目录)
TARGET_DIRS = [
    "cmd/server",
    "internal/auth",
    "internal/domain",
    "internal/handler/http",
    "internal/handler/websocket",
    "internal/hub",
    "internal/infra/persistence/gorm",
    "internal/infra/setup",
    "internal/infra/state/redis",
    "internal/middleware",
    "internal/repository",
    "internal/service",
    "internal/tasks",
    "internal/worker",
    "internal/bootstrap",
]

print(f"开始收集 Go 代码到 {OUTPUT_FILE} ...")

try:
    with open(OUTPUT_FILE, 'w', encoding='utf-8') as outfile:
        # 遍历目标目录
        for dir_name in TARGET_DIRS:
            dir_path = pathlib.Path(PROJECT_ROOT) / dir_name
            # 检查目录是否存在
            if dir_path.is_dir():
                # 递归查找所有 .go 文件
                for file_path in dir_path.rglob("*.go"):
                    if file_path.is_file():
                        print(f"正在处理: {file_path}")
                        # 写入文件路径分隔符
                        outfile.write(f"\n\n--- 文件: {file_path} ---\n\n")
                        # 读取并写入文件内容
                        try:
                            with open(file_path, 'r', encoding='utf-8') as infile:
                                outfile.write(infile.read())
                        except Exception as e:
                            print(f"警告: 读取文件 {file_path} 时出错: {e}")
                            outfile.write(f"*** 读取文件时出错: {e} ***\n")
            else:
                print(f"警告: 目录 {dir_path} 未找到，已跳过。")

    print(f"代码收集完成。输出已保存到 {OUTPUT_FILE}")

except IOError as e:
    print(f"错误: 无法写入输出文件 {OUTPUT_FILE}: {e}")
except Exception as e:
    print(f"发生意外错误: {e}")