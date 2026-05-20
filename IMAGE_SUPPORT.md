# Image Support in x10

x10 now supports image input via stdin for both Claude (Anthropic) and GPT models. This allows you to analyze screenshots, diagrams, error dialogs, and other images directly from the CLI.

## Supported Image Formats

- **JPEG** (.jpg, .jpeg)
- **PNG** (.png)
- **GIF** (.gif)
- **WebP** (.webp)

Maximum file size: 5MB per image

## Usage

### Basic Image Analysis

Pipe an image and provide a task:

```bash
# Analyze a screenshot
cat screenshot.png | x10 "what's wrong with the UI?"

# Analyze an error dialog
x10 "fix this error" < error_dialog.jpg

# From a file
x10 "describe this diagram" < diagram.png
```

### With Arguments

You can use the `run` command with both image input and arguments:

```bash
# Analyze with a specific model
cat screenshot.png | x10 -m claude-opus-4-6 "find accessibility issues"

# With multiple agents (each gets the image)
cat architecture.png | x10 -n 3 "describe the components"

# In a specific directory
cat bug.png | x10 -d /path/to/project "locate this bug in the codebase"
```

### Interactive Mode

You can also use image input in interactive REPL:

```bash
x10 run
# Then pipe image when prompted:
# (in another terminal)
cat screenshot.png | x10 run
```

## Implementation Details

### Image Detection

The CLI automatically detects image format by reading magic bytes:
- JPEG: `0xFF 0xD8 0xFF`
- PNG: `0x89 0x50 0x4E 0x47`
- GIF: `0x47 0x49 0x46 0x38`
- WebP: `0x52 0x49 0x46 0x46 ... 0x57 0x45 0x42 0x50`

### Data Flow

1. **Detection** → stdin is read to detect image format
2. **Encoding** → Image is base64-encoded (up to 5MB)
3. **Wrapping** → Image data is wrapped in `[IMAGE]...[END_IMAGE]` block
4. **Parsing** → Agent parses the block and builds multi-block content
5. **Provider** → Anthropic or OpenAI provider converts blocks to proper format

### Content Block Format

Images are sent to LLMs as multi-block content:

```
[
  {
    "type": "image",
    "source": {
      "type": "base64",
      "media_type": "image/png",
      "data": "<base64-encoded-image>"
    }
  },
  {
    "type": "text", 
    "text": "user's task text"
  }
]
```

### Codebase Context with Images

When an image is analyzed along with the index enabled, the agent will:
1. Receive the image data
2. Pre-load relevant codebase context
3. Combine both as content blocks
4. Make an informed analysis

```bash
# Analyze screenshot with full codebase context
cat error_screenshot.png | x10 "what code caused this?"
# → Agent sees image + relevant code + system prompt
```

## Provider Support

### Anthropic (Claude)

Images are sent as content blocks in Anthropic's message format:
- **Type**: `image`
- **Source type**: `base64`
- **Supported models**: Claude 3 family and newer

```json
{
  "type": "image",
  "source": {
    "type": "base64",
    "media_type": "image/png",
    "data": "..."
  }
}
```

### OpenAI (GPT models)

Images are sent as content blocks with data URIs:
- **Type**: `image_url`
- **URL format**: `data:<media-type>;base64,<data>`
- **Supported models**: GPT-4 Vision, GPT-4o, and newer

```json
{
  "type": "image_url",
  "image_url": {
    "url": "data:image/png;base64,..."
  }
}
```

## Error Handling

Common issues and solutions:

| Issue | Cause | Solution |
|-------|-------|----------|
| "not a recognized image format" | Unknown format or corrupted file | Ensure file is valid JPEG, PNG, GIF, or WebP |
| "file too large" (implied) | Image exceeds 5MB | Compress the image first |
| Model doesn't recognize image | Old model version | Use Claude 3+, GPT-4V+, or GPT-4o |
| Image appears as text in output | Piping failed | Verify stdin with `cat file.png | file -` |

## Examples

### UI Bug Analysis

```bash
# Screenshot → Claude analyzes → returns fix suggestions
cat screenshot.png | x10 -m claude-opus-4-6 "this button is misaligned, fix it in the CSS"
```

### Diagram Explanation

```bash
# Architecture diagram → GPT-4o explains components
cat architecture.png | x10 -m gpt-4o "describe the data flow"
```

### Multi-Agent Analysis

```bash
# Same screenshot analyzed by 3 agents in parallel
cat error.png | x10 -n 3 "independent analysis of this error"
```

### Combination with Codebase

```bash
# Error dialog → Agent finds & fixes the code
cat error_dialog.png | x10 "find and fix what caused this error"
# Agent:
# 1. Sees the error message in image
# 2. Has codebase indexed
# 3. Searches for matching code
# 4. Makes targeted fix
```

## Performance Notes

- **Image detection**: ~5ms (magic byte check)
- **Base64 encoding**: ~50ms per MB
- **Provider overhead**: Standard API call latency
- **Advantage**: Image data sent in single request (no round trips)

## Limitations

1. **Single image per task**: Each task supports one image (multiple in future)
2. **Image metadata**: Only pixel data is sent (EXIF, etc. stripped)
3. **Model-specific**: Requires vision-capable models
4. **Size constraint**: 5MB max (sufficient for screenshots/diagrams)

## Future Enhancements

- [ ] Multiple images per task
- [ ] Image cropping/region selection
- [ ] Integration with screenshot tools (`scrot`, `import`)
- [ ] Clipboard image input (`xclip`, `pbpaste`)
- [ ] Video frame extraction
- [ ] OCR preprocessing for text-heavy images

## Technical References

- [Anthropic Vision API](https://docs.anthropic.com/claude/reference/vision)
- [OpenAI Vision Guide](https://platform.openai.com/docs/guides/vision)
- [Base64 Encoding (RFC 4648)](https://tools.ietf.org/html/rfc4648)
