// 运行时配置（本地开发默认值，Docker 环境会被 entrypoint 脚本覆盖）
window.__RUNTIME_CONFIG__ = {
  // Desktop 单机部署没有 nginx 前置的 client_max_body_size，这里的值就是前端
  // 唯一的上传大小闸门，必须与部署目录 .env 里的 MAX_FILE_SIZE_MB 保持一致
  // （10240 = 10 GB，实际只受内存与磁盘限制）。
  MAX_FILE_SIZE_MB: 10240,
  MAX_SKILL_BUNDLE_SIZE_MB: 256,
  // Optional: serve embed on a dedicated origin, e.g. 'https://embed.example.com'
  EMBED_BASE_URL: '',
  // Optional: default UI locale for first-time visitors (zh-CN | zh-TW | en-US | ru-RU | ko-KR | ja-JP)
  DEFAULT_LOCALE: '',
};
