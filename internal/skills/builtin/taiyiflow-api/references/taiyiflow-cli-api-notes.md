# TaiyiFlow CLI Bundled Skill Notes

- 当前 bundled skills 会通过 embed 打包进 CLI 二进制。
- `skill` 本地工具返回 `source`、`path`、`root_dir`、`relative_dir` 和 `files`，供上层推理或提示词拼接使用。
- 对包含 `files` frontmatter 的 bundled skill，参考文件会被提取到 `SNZ1DP_HOME/bundled-skills/<skill-name>/` 下。
- `taiyiflow-api` 当前仅提供内置参考资料，不直接访问外部网络或浏览器。