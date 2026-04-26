---
description: "Use when handling file_edit PATCH_CONFLICT results, old_string mismatch recovery, prefetched_read, retry_hint.recommended_args, or expected_sha256_before reuse. Covers safe retry behavior for local file editing."
---
# file_edit 冲突恢复指引

- 当 `file_edit` 返回 `PATCH_CONFLICT` 时，不要直接重复原请求。
- 优先读取结果中的 `retry_hint.recommended_args`，并将其中的 `path`、`old_string`、`expected_sha256_before` 作为下一次 `file_edit` 的基础参数。
- 下一次 `file_edit` 时，只补充或更新真正缺失的字段，通常只需要提供新的 `new_string`，不要随意改写 `recommended_args.old_string`。
- 优先使用结果中的 `prefetched_read` 理解上下文；只有当 `prefetched_read` 缺失、内容明显不足，或需要更大范围上下文时，才额外调用 `read_file`。
- 如果 `prefetched_read` 与当前目标不一致，先重新 `read_file`，再用新的精确片段和最新 `expected_sha256_before` 组织下一次 `file_edit`。
- 当冲突原因是多处命中时，优先缩小 `old_string` 范围；只有在确实要全量替换时才设置 `replace_all=true`。
- 不要因为 `PATCH_CONFLICT` 直接退回到 `write_file` 全量覆盖，除非用户明确要求覆盖整个文件。