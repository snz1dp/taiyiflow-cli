---
name: remote-file-access
description: 当需要访问、引用或下载服务端产物文件时，使用正确的 URL 拼接规则构造下载地址。
category: platform
metadata:
  request_types:
    - ask
    - plan
    - agent
---

# 服务端文件产物访问规则

当对话中涉及服务端产物文件（如之前会话生成的文档、图片、代码文件等），且需要构造可访问的 URL 时，必须遵循以下拼接规则。

## URL 拼接规则

服务端文件下载基础地址：

```
{{SERVER_DOWNLOAD_URL}}
```

完整下载地址 = 上述基础地址 + 文件路径（不带前导斜杠）。

示例：若服务端产物路径为 `agent-001/sess-002/report.docx`，则完整 URL 为：

```
{{SERVER_DOWNLOAD_URL}}agent-001/sess-002/report.docx
```

## 认证方式

下载服务端产物必须携带有效的单点登录令牌。由于令牌有时效性，**每次需要下载时必须调用 `get_sso_token` 工具获取最新令牌**，不要缓存或复用之前获取的令牌。

获取令牌后，选择以下任一方式附加认证：

1. **URL query 参数**（推荐，适用于浏览器打开或 curl 下载）：
   ```
   {下载URL}?__TOKEN__={令牌值}
   ```
   若 URL 已有其他 query 参数，使用 `&__TOKEN__={令牌值}` 追加。

2. **HTTP 请求头**（适用于程序化请求）：
   ```
   Authorization: Bearer {令牌值}
   ```

## 注意事项

- 文件路径中如果包含空格、中文或特殊字符，需对每段路径做 URL 编码。
- 令牌有时效性，每次构造下载请求前都应重新调用 `get_sso_token` 获取。
- 不要将本地文件系统路径与此规则混淆：本规则仅适用于服务端存储的产物文件。
