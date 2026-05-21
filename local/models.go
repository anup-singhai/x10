package local

// Model is a curated SLM entry in the x10 catalog.
type Model struct {
	ID       string // alias used with -m  (e.g. "qwen-coder")
	Name     string // display name
	Desc     string // one-liner
	Size     string // approximate download size
	HFRepo   string // HuggingFace "org/repo"
	HFFile   string // exact GGUF filename inside the repo
	CtxLen   int    // context window (tokens)
	ToolCall bool   // model supports OpenAI-style function/tool calling
}

// Catalog is x10's curated list of SLMs optimised for coding and agentic use.
// All models are Q4_K_M quantised GGUF files — good balance of speed/quality.
//
// Selection criteria:
//   - Strong function-calling support (needed for x10 tools)
//   - Coding-specific training or strong general coding capability
//   - Small enough to run on a laptop CPU (≤8 GB RAM)
var Catalog = []Model{
	{
		ID:       "qwen-coder",
		Name:     "Qwen2.5-Coder 1.5B",
		Desc:     "Best coding accuracy at 1.5B — fast on CPU, great tool use",
		Size:     "~1.1 GB",
		HFRepo:   "Qwen/Qwen2.5-Coder-1.5B-Instruct-GGUF",
		HFFile:   "qwen2.5-coder-1.5b-instruct-q4_k_m.gguf",
		CtxLen:   32768,
		ToolCall: true,
	},
	{
		ID:       "qwen-coder-7b",
		Name:     "Qwen2.5-Coder 7B",
		Desc:     "Best open-source coding SLM — rivals GPT-4 on HumanEval",
		Size:     "~4.7 GB",
		HFRepo:   "Qwen/Qwen2.5-Coder-7B-Instruct-GGUF",
		HFFile:   "qwen2.5-coder-7b-instruct-q4_k_m.gguf",
		CtxLen:   131072,
		ToolCall: true,
	},
	{
		ID:       "phi4-mini",
		Name:     "Phi-4 Mini 3.8B",
		Desc:     "Microsoft Phi-4 Mini — top reasoning at 3.8B, tool calling",
		Size:     "~2.5 GB",
		HFRepo:   "microsoft/Phi-4-mini-instruct-gguf",
		HFFile:   "Phi-4-mini-instruct-Q4_K_M.gguf",
		CtxLen:   16384,
		ToolCall: true,
	},
	{
		ID:       "deepseek-coder",
		Name:     "DeepSeek-Coder-V2 Lite",
		Desc:     "MoE coding model — 2.4B active params, strong at multi-file edits",
		Size:     "~3.0 GB",
		HFRepo:   "bartowski/DeepSeek-Coder-V2-Lite-Instruct-GGUF",
		HFFile:   "DeepSeek-Coder-V2-Lite-Instruct-Q4_K_M.gguf",
		CtxLen:   163840,
		ToolCall: true,
	},
	{
		ID:       "smollm",
		Name:     "SmolLM2 1.7B",
		Desc:     "HuggingFace SmolLM2 — ultra-fast, good for quick offline tasks",
		Size:     "~1.1 GB",
		HFRepo:   "HuggingFaceTB/SmolLM2-1.7B-Instruct-GGUF",
		HFFile:   "smollm2-1.7b-instruct-q4_k_m.gguf",
		CtxLen:   8192,
		ToolCall: false,
	},
}

// FindModel looks up a model by its ID alias.
func FindModel(id string) (Model, bool) {
	for _, m := range Catalog {
		if m.ID == id {
			return m, true
		}
	}
	return Model{}, false
}
