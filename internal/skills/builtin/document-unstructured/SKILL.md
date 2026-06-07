---
name: document-unstructured
description: 在独立本地音频、视频、PDF 等非图片文件需要服务端结构化解析的场景下，指导优先使用 local.document_unstructured。
category: tools
metadata:
  request_types:
    - agent
---

当任务目标是从本地文件中提取正文、字幕、转写或结构化结果时，优先考虑 `local.document_unstructured`，而不是把文件当作普通附件直接上传。

适用线索：
- 音频转写、会议录音整理
- 视频字幕、语音内容提取
- PDF 或其他需要结构化抽取正文的文档

调用前检查：
- 输入是本地文件路径，优先使用 `path` 或 `paths`
- 当前配置已启用对应的 `doc_unstructured_exts`
- 服务端已配置 `doc_unstructured_flow_id`
- 需要把抽取目标或附加说明放进 `input`

不适用场景：
- 纯文本、源码、Markdown 这类可直接 `read_file` 的内容
- 只想把文件作为普通上下文附件上传，而不需要结构化 JSON 结果
- 独立本地图片 OCR、扫描件、静态截图文件文字提取；这类输入默认应改用 `local.image_understanding`
- 图片来自 `taiyiflow-in-chrome__screenshot` 或 `taiyiflow-computer__screenshot` 的返回结果；这两类截图应优先使用其自带的 `image_understanding`

失败回退：
- 如果扩展名未启用，先提示需要调整 `doc_unstructured_exts`
- 如果服务端不支持该类型，再回退到普通文件上传或改用其他本地工具
