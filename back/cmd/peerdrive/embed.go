// embed.go: the `peerdrive embed` subcommand — a one-shot CLI against a
// standalone EmbeddingGemma service.
//
// It exists because the module is aimed at "自己开着玩": you run
// `llama-server -m embeddinggemma.gguf --embedding` on this box or somewhere
// else and want to check what it does without writing a curl command or
// standing up the HTTP server. Nothing here starts a listener; it shares the
// same embedding.Client the HTTP routes use, so the CLI and the endpoint can
// never disagree about the wire format.
//
// Exit codes: 0 = ok, 1 = failure (module disabled, unreachable, bad input).
// One code on purpose: `peerdrive embed` is a building block, so callers get a
// plain success/failure signal rather than a taxonomy.

package main

import (
	"bufio"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"peerdrive/internal/config"
	"peerdrive/internal/embedding"
)

func runEmbed(args []string) int {
	fs := flag.NewFlagSet("embed", flag.ExitOnError)
	url := fs.String("url", "", "embeddings endpoint (default $PEERDRIVE_EMBEDDING_URL)")
	model := fs.String("model", "", "model name sent in the request (default $PEERDRIVE_EMBEDDING_MODEL)")
	timeout := fs.Int("timeout", 0, "per-request timeout in seconds (default $PEERDRIVE_EMBEDDING_TIMEOUT_SECS)")
	apiKey := fs.String("key", "", "sent as Authorization: Bearer (default $PEERDRIVE_EMBEDDING_API_KEY)")
	dim := fs.Int("dim", -1, "expected vector dimension: -1 = use $PEERDRIVE_EMBEDDING_DIM, 0 = disable the check, N = N")
	format := fs.String("format", "json", "output format: json | vec")
	ping := fs.Bool("ping", false, "probe reachability only; no vector is printed")
	fs.Parse(args)

	// Go's flag package stops at the first positional argument, so
	// `peerdrive embed "text" -url X` silently treats -url as another text and
	// the flag never reaches us — the request then goes to the default URL and
	// fails in a confusing way. Flags first, text after.
	if pos := fs.Args(); len(pos) > 0 {
		for _, a := range pos[1:] {
			if len(a) > 1 && a[0] == '-' {
				fmt.Fprintln(os.Stderr, "peerdrive embed: flags must come before the text")
				fmt.Fprintln(os.Stderr, "  bad:  peerdrive embed \"text\" -url X")
				fmt.Fprintln(os.Stderr, "  good: peerdrive embed -url X \"text\"")
				return 1
			}
		}
	}

	cfg := config.Load()

	// Precedence: flag > env > compiled-in default. `fs.Visit` reports only the
	// flags the user actually typed, so a `-dim 0` is distinguishable from the
	// unset default.
	visit := map[string]bool{}
	fs.Visit(func(f *flag.Flag) { visit[f.Name] = true })

	finalURL := cfg.EmbeddingURL
	if visit["url"] {
		finalURL = *url
	}
	finalModel := cfg.EmbeddingModel
	if visit["model"] {
		finalModel = *model
	}
	finalTimeout := cfg.EmbeddingTimeoutSecs
	if visit["timeout"] {
		finalTimeout = *timeout
	}
	finalKey := cfg.EmbeddingAPIKey
	if visit["key"] {
		finalKey = *apiKey
	}
	finalDim := cfg.EmbeddingExpectedDim
	if visit["dim"] {
		finalDim = *dim
	}

	client := embedding.New(embedding.Config{
		URL:         finalURL,
		Model:       finalModel,
		TimeoutSecs: finalTimeout,
		APIKey:      finalKey,
		ExpectedDim: finalDim,
	})

	ctx := context.Background()
	if *ping {
		start := time.Now()
		if err := client.Ping(ctx); err != nil {
			fmt.Fprintf(os.Stderr, "peerdrive embed: unreachable after %s: %v\n", time.Since(start).Round(time.Millisecond), err)
			return 1
		}
		fmt.Printf("ok model=%s dim=%d url=%s elapsed=%s\n",
			client.Model(), finalDim, embedding.URLForStatus(client.URL()), time.Since(start).Round(time.Millisecond))
		return 0
	}

	texts, err := collectTexts(fs.Args())
	if err != nil {
		fmt.Fprintf(os.Stderr, "peerdrive embed: %v\n", err)
		usageEmbed(os.Stderr)
		return 1
	}

	start := time.Now()
	if len(texts) == 1 {
		vec, err := client.Embed(ctx, texts[0])
		if err != nil {
			fmt.Fprintf(os.Stderr, "peerdrive embed: %v\n", err)
			return 1
		}
		return printOne(*format, client.Model(), vec, time.Since(start).Milliseconds())
	}

	vecs, err := client.EmbedBatch(ctx, texts)
	if err != nil {
		fmt.Fprintf(os.Stderr, "peerdrive embed: %v\n", err)
		return 1
	}
	return printMany(*format, client.Model(), vecs, time.Since(start).Milliseconds())
}

// collectTexts gathers the texts to embed: positional arguments, or everything
// on stdin when nothing was passed and stdin is a pipe or a regular file
// (so `cat corpus.txt | peerdrive embed` works).
func collectTexts(args []string) ([]string, error) {
	if len(args) > 0 {
		return args, nil
	}
	stat, err := os.Stdin.Stat()
	if err != nil {
		return nil, fmt.Errorf("cannot read stdin: %w", err)
	}
	if (stat.Mode() & os.ModeCharDevice) != 0 {
		return nil, fmt.Errorf("no text given: pass text as arguments, or pipe it via stdin")
	}
	raw, err := io.ReadAll(os.Stdin)
	if err != nil {
		return nil, fmt.Errorf("cannot read stdin: %w", err)
	}
	var texts []string
	for _, line := range strings.Split(string(raw), "\n") {
		if line = strings.TrimSpace(line); line != "" {
			texts = append(texts, line)
		}
	}
	if len(texts) == 0 {
		return nil, fmt.Errorf("no text given: pass text as arguments, or pipe lines via stdin")
	}
	return texts, nil
}

// printOne writes one vector. json is the default: complete and self-describing.
// vec is the compact form (space-separated floats, one line) for piping into
// another tool.
func printOne(format, model string, vec []float32, elapsedMs int64) int {
	switch format {
	case "json":
		b, err := json.Marshal(map[string]any{
			"model":      model,
			"dim":        len(vec),
			"elapsed_ms": elapsedMs,
			"embedding":  vec,
		})
		if err != nil {
			fmt.Fprintf(os.Stderr, "peerdrive embed: encode: %v\n", err)
			return 1
		}
		fmt.Println(string(b))
		return 0
	case "vec":
		return printVector(vec)
	default:
		fmt.Fprintf(os.Stderr, "peerdrive embed: unknown -format %q (json | vec)\n", format)
		return 1
	}
}

// printMany writes one vector per input, paired with the input's position.
func printMany(format, model string, vecs [][]float32, elapsedMs int64) int {
	switch format {
	case "json":
		// Guard: EmbedBatch never returns an empty slice for non-empty input,
		// but the JSON shape should not panic if it ever does.
		dim := 0
		if len(vecs) > 0 {
			dim = len(vecs[0])
		}
		b, err := json.Marshal(map[string]any{
			"model":      model,
			"dim":        dim,
			"count":      len(vecs),
			"elapsed_ms": elapsedMs,
			"embeddings": vecs,
		})
		if err != nil {
			fmt.Fprintf(os.Stderr, "peerdrive embed: encode: %v\n", err)
			return 1
		}
		fmt.Println(string(b))
		return 0
	case "vec":
		w := bufio.NewWriter(os.Stdout)
		for i, v := range vecs {
			if err := printVectorW(w, fmt.Sprintf("%d", i), v); err != nil {
				fmt.Fprintf(os.Stderr, "peerdrive embed: encode: %v\n", err)
				return 1
			}
		}
		if err := w.Flush(); err != nil {
			fmt.Fprintf(os.Stderr, "peerdrive embed: flush: %v\n", err)
			return 1
		}
		return 0
	default:
		fmt.Fprintf(os.Stderr, "peerdrive embed: unknown -format %q (json | vec)\n", format)
		return 1
	}
}

// printVector writes one space-separated vector to stdout.
func printVector(vec []float32) int {
	w := bufio.NewWriter(os.Stdout)
	if err := printVectorW(w, "", vec); err != nil {
		fmt.Fprintf(os.Stderr, "peerdrive embed: encode: %v\n", err)
		return 1
	}
	if err := w.Flush(); err != nil {
		fmt.Fprintf(os.Stderr, "peerdrive embed: flush: %v\n", err)
		return 1
	}
	return 0
}

// printVectorW writes [prefix]<tab><space-separated floats> to w. An empty
// prefix gives a bare vector, which is what the single-text form wants.
func printVectorW(w *bufio.Writer, prefix string, vec []float32) error {
	parts := make([]string, len(vec))
	for i, f := range vec {
		parts[i] = fmt.Sprintf("%.7g", f)
	}
	line := strings.Join(parts, " ")
	if prefix != "" {
		line = prefix + "\t" + line
	}
	_, err := fmt.Fprintln(w, line)
	return err
}

func usageEmbed(w io.Writer) {
	fmt.Fprint(w, `
peerdrive embed — one-shot embedding against a standalone EmbeddingGemma service

用法:
  peerdrive embed "some text"                 单条文本 → 768 维向量
  peerdrive embed "a" "b" "c"                 批量
  cat corpus.txt | peerdrive embed            从 stdin 逐行读
  peerdrive embed --ping                      只探测可达性
  peerdrive embed "t" -format vec             紧凑输出（空格分隔，便于管道）

注意：flag 必须写在文本前面。Go 的 flag 包遇到第一个非 flag 参数就停止解析，
peerdrive embed "text" -url X 里的 -url 会被当成第二段文本，请求会打到默认
地址上然后报一个莫名其妙的错。

配置优先级: flag > 环境变量 > 默认值
  PEERDRIVE_EMBEDDING_ENABLE  = true              模块总开关（serve 时才需要）
  PEERDRIVE_EMBEDDING_URL     = http://127.0.0.1:8080/v1/embeddings
  PEERDRIVE_EMBEDDING_MODEL   = embeddinggemma-2
  PEERDRIVE_EMBEDDING_TIMEOUT_SECS = 30
  PEERDRIVE_EMBEDDING_API_KEY =                   空 = 不发 Authorization
  PEERDRIVE_EMBEDDING_DIM     = 768               0 = 不做维度校验

服务端自己开（peerdrive 不做本地推理）:
  llama-server -m embeddinggemma.gguf --embedding        # 默认 http://127.0.0.1:8080
  ollama serve                                           # 用 /v1/embeddings 兼容层

退出码: 0 = 成功, 1 = 失败
`)
}
