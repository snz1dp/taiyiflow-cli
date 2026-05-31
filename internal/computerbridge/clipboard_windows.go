//go:build windows

package computerbridge

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

type windowsClipboardSnapshot struct {
	Formats     []windowsClipboardFormat `json:"formats"`
	HadData     bool                     `json:"had_data"`
	Unsupported []string                 `json:"unsupported,omitempty"`
}

type windowsClipboardFormat struct {
	Format   string   `json:"format"`
	Kind     string   `json:"kind"`
	Text     string   `json:"text,omitempty"`
	Values   []string `json:"values,omitempty"`
	BytesB64 string   `json:"bytes_b64,omitempty"`
}

func windowsCaptureClipboard(ctx context.Context) ([]byte, error) {
	output, err := windowsRunPowerShell(ctx, windowsClipboardCaptureScript, "读取 Windows 剪贴板快照失败", "")
	if err != nil {
		return nil, err
	}
	trimmed := strings.TrimSpace(output)
	if trimmed == "" {
		trimmed = `{"formats":[]}`
	}
	var snapshot windowsClipboardSnapshot
	if err := json.Unmarshal([]byte(trimmed), &snapshot); err != nil {
		return nil, fmt.Errorf("解析 Windows 剪贴板快照失败: %w", err)
	}
	if snapshot.HadData && len(snapshot.Unsupported) > 0 {
		return nil, fmt.Errorf("当前 Windows 剪贴板包含暂不支持恢复的格式: %s", strings.Join(snapshot.Unsupported, ", "))
	}
	normalized, err := json.Marshal(snapshot)
	if err != nil {
		return nil, fmt.Errorf("序列化 Windows 剪贴板快照失败: %w", err)
	}
	return normalized, nil
}

func windowsRestoreClipboard(ctx context.Context, snapshot []byte) error {
	payload := snapshot
	if len(payload) == 0 {
		payload = []byte(`{"formats":[]}`)
	}
	if !json.Valid(payload) {
		return fmt.Errorf("Windows 剪贴板快照格式无效")
	}
	_, err := windowsRunPowerShell(ctx, windowsClipboardRestoreScript, "恢复 Windows 剪贴板失败", string(payload))
	return err
}

const windowsClipboardCaptureScript = `
Add-Type -AssemblyName System.Windows.Forms
Add-Type -AssemblyName System.Drawing
[Console]::OutputEncoding = [System.Text.UTF8Encoding]::UTF8
$dataObject = [System.Windows.Forms.Clipboard]::GetDataObject()
$items = New-Object System.Collections.Generic.List[object]
$unsupported = New-Object System.Collections.Generic.List[string]
$hadData = $false
if ($null -ne $dataObject) {
  $formats = @($dataObject.GetFormats($true))
  if ($formats.Count -gt 0) { $hadData = $true }
  foreach ($format in $formats) {
    if (-not $dataObject.GetDataPresent($format, $true)) { continue }
    $data = $dataObject.GetData($format, $true)
    if ($null -eq $data) { continue }
    if ($data -is [string]) {
      $items.Add([pscustomobject]@{ format = $format; kind = 'string'; text = [string]$data })
      continue
    }
    if ($data -is [string[]]) {
      $items.Add([pscustomobject]@{ format = $format; kind = 'string_array'; values = @($data | ForEach-Object { [string]$_ }) })
      continue
    }
    if ($data -is [System.Collections.Specialized.StringCollection]) {
      $values = @()
      foreach ($value in $data) { $values += [string]$value }
      $items.Add([pscustomobject]@{ format = $format; kind = 'string_collection'; values = $values })
      continue
    }
    if ($data -is [byte[]]) {
      $items.Add([pscustomobject]@{ format = $format; kind = 'bytes'; bytes_b64 = [Convert]::ToBase64String($data) })
      continue
    }
    if ($data -is [System.IO.MemoryStream]) {
      $items.Add([pscustomobject]@{ format = $format; kind = 'stream'; bytes_b64 = [Convert]::ToBase64String($data.ToArray()) })
      continue
    }
    if ($data -is [System.IO.Stream]) {
      $memory = New-Object System.IO.MemoryStream
      $data.CopyTo($memory)
      $items.Add([pscustomobject]@{ format = $format; kind = 'stream'; bytes_b64 = [Convert]::ToBase64String($memory.ToArray()) })
      continue
    }
    if ($data -is [System.Drawing.Image]) {
      $memory = New-Object System.IO.MemoryStream
      $data.Save($memory, [System.Drawing.Imaging.ImageFormat]::Png)
      $items.Add([pscustomobject]@{ format = $format; kind = 'png_image'; bytes_b64 = [Convert]::ToBase64String($memory.ToArray()) })
      continue
    }
    $unsupported.Add([string]$format)
  }
}
$result = [pscustomobject]@{ formats = $items; had_data = $hadData; unsupported = @($unsupported) }
[Console]::Out.Write(($result | ConvertTo-Json -Compress -Depth 8))
`

const windowsClipboardRestoreScript = `
Add-Type -AssemblyName System.Windows.Forms
Add-Type -AssemblyName System.Drawing
[Console]::InputEncoding = [System.Text.UTF8Encoding]::UTF8
$json = [Console]::In.ReadToEnd()
if ([string]::IsNullOrWhiteSpace($json)) { $json = '{"formats":[]}' }
$payload = $json | ConvertFrom-Json -Depth 8
$formats = @()
if ($null -ne $payload -and $null -ne $payload.formats) {
  $formats = @($payload.formats)
}
if ($formats.Count -eq 0) {
  [System.Windows.Forms.Clipboard]::Clear()
  exit 0
}
$dataObject = New-Object System.Windows.Forms.DataObject
$keepAlive = New-Object System.Collections.Generic.List[object]
foreach ($entry in $formats) {
  $format = [string]$entry.format
  switch ([string]$entry.kind) {
    'string' {
      $dataObject.SetData($format, $true, [string]$entry.text)
    }
    'string_array' {
      $values = @()
      if ($null -ne $entry.values) { $values = @($entry.values | ForEach-Object { [string]$_ }) }
      $dataObject.SetData($format, $true, [string[]]$values)
    }
    'string_collection' {
      $collection = New-Object System.Collections.Specialized.StringCollection
      if ($null -ne $entry.values) {
        foreach ($value in @($entry.values)) { [void]$collection.Add([string]$value) }
      }
      $dataObject.SetData($format, $true, $collection)
      [void]$keepAlive.Add($collection)
    }
    'bytes' {
      $dataObject.SetData($format, $true, [Convert]::FromBase64String([string]$entry.bytes_b64))
    }
    'stream' {
      $bytes = [Convert]::FromBase64String([string]$entry.bytes_b64)
      $stream = New-Object System.IO.MemoryStream(, $bytes)
      $dataObject.SetData($format, $true, $stream)
      [void]$keepAlive.Add($stream)
    }
    'png_image' {
      $bytes = [Convert]::FromBase64String([string]$entry.bytes_b64)
      $stream = New-Object System.IO.MemoryStream(, $bytes)
      $image = [System.Drawing.Image]::FromStream($stream)
      $dataObject.SetData($format, $true, $image)
      [void]$keepAlive.Add($stream)
      [void]$keepAlive.Add($image)
    }
  }
}
[System.Windows.Forms.Clipboard]::SetDataObject($dataObject, $true)
[GC]::KeepAlive($keepAlive)
`
