FROM snz1.cn/dp/vueapp:2.0

ENV TZ=Asia/Shanghai

COPY files/ /app/html/
COPY build/release/ /app/html/downloads/
COPY nginx/default.conf /etc/nginx/conf.d/

RUN set -eu; \
	downloads_dir=/app/html/downloads; \
	if [ ! -d "$downloads_dir" ] || [ -z "$(find "$downloads_dir" -mindepth 1 -maxdepth 1 -type f -print -quit)" ]; then \
		echo "build/release is empty; run make release before building the download image" >&2; \
		exit 1; \
	fi; \
	checksum_file="$downloads_dir/SHA256SUMS"; \
	tmp_manifest=/tmp/taiyiflow-cli-manifest.json; \
	: > "$checksum_file"; \
	first=1; \
	printf '{\n  "project": "taiyiflow-cli",\n  "generated_at": "%s",\n  "artifacts": [\n' "$(date +%FT%T.%3N%z)" > "$tmp_manifest"; \
	for file in $(find "$downloads_dir" -mindepth 1 -maxdepth 1 -type f | sort); do \
		base=$(basename "$file"); \
		if [ "$base" = "SHA256SUMS" ]; then \
			continue; \
		fi; \
		checksum=$(sha256sum "$file" | awk '{print $1}'); \
		size=$(wc -c < "$file" | tr -d ' '); \
		printf '%s  %s\n' "$checksum" "$base" >> "$checksum_file"; \
		if [ "$first" -eq 0 ]; then \
			printf ',\n' >> "$tmp_manifest"; \
		fi; \
		printf '    {"name":"%s","path":"downloads/%s","sha256":"%s","size_bytes":%s}' "$base" "$base" "$checksum" "$size" >> "$tmp_manifest"; \
		first=0; \
	done; \
	printf '\n  ],\n  "checksums": "downloads/SHA256SUMS"\n}\n' >> "$tmp_manifest"; \
	mv "$tmp_manifest" /app/html/manifest.json; \
	printf '{"time":"%s"}\n' "$(date +%FT%T.%3N%z)" > /app/html/BUILD
