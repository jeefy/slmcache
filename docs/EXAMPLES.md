# Examples

## Chatbot With slmcache

The snippet below demonstrates how to wrap slmcache in front of a (mocked) large
language model. The flow is:

1. Embed the user's prompt locally and look up the cache via the HTTP API.
2. If a near match is found (similarity above the server's threshold), return
   the cached answer.
3. Otherwise, call a placeholder `callLLM` function, write the new prompt/response
   back to slmcache, and return the fresh answer to the user.

```go
package main

import (
    "bytes"
    "encoding/json"
    "fmt"
    "io"
    "log"
    "net/http"
    "net/url"
    "time"
)

type entry struct {
    ID       int64                  `json:"id,omitempty"`
    Prompt   string                 `json:"prompt"`
    Response string                 `json:"response"`
    Metadata map[string]interface{} `json:"metadata,omitempty"`
}

type chatbot struct {
    cacheURL string
}

func newChatbot(cacheURL string) *chatbot { return &chatbot{cacheURL: cacheURL} }

func (c *chatbot) chat(prompt string) (string, error) {
    // Step 1: query slmcache for similar prompts.
    searchURL := fmt.Sprintf("%s/search?q=%s&limit=3", c.cacheURL, url.QueryEscape(prompt))
    res, err := http.Get(searchURL)
    if err != nil {
        return "", fmt.Errorf("search failed: %w", err)
    }
    defer res.Body.Close()
    if res.StatusCode != http.StatusOK {
        body, _ := io.ReadAll(res.Body)
        return "", fmt.Errorf("search status %d: %s", res.StatusCode, body)
    }

    var matches []entry
    if err := json.NewDecoder(res.Body).Decode(&matches); err != nil {
        return "", fmt.Errorf("decode search response: %w", err)
    }

    if len(matches) > 0 {
        // Cache hit! The first result already passed the server-side similarity threshold.
        return matches[0].Response, nil
    }

    // Step 2: Miss — call the slow LLM (mocked for this example).
    response := callLLM(prompt)

    // Step 3: Store the new pair back inside slmcache for the next request.
    payload := entry{Prompt: prompt, Response: response}
    buf, _ := json.Marshal(payload)
    createRes, err := http.Post(c.cacheURL+"/entries", "application/json", bytes.NewReader(buf))
    if err != nil {
        return response, nil // return the response but log the error
    }
    createRes.Body.Close()
    if createRes.StatusCode != http.StatusCreated {
        log.Printf("slmcache create returned %d", createRes.StatusCode)
    }
    return response, nil
}

func callLLM(prompt string) string {
    // Replace this with a real LLM call. Here we simulate latency.
    time.Sleep(250 * time.Millisecond)
    return "[LLM answer] " + prompt
}

func main() {
    bot := newChatbot("http://localhost:8080")
    for _, prompt := range []string{
        "Where is KubeCon 2025?",
        "Which city hosts KubeCon this year?", // cache hit after first call
    } {
        resp, err := bot.chat(prompt)
        if err != nil {
            log.Fatalf("chat error: %v", err)
        }
        fmt.Printf("Prompt: %s\nResponse: %s\n\n", prompt, resp)
    }
}
```

Run the example with a locally running slmcache service (see `README.md` for
startup instructions). The first prompt populates the cache, and the second
prompt reuses the cached answer immediately instead of hitting the mock LLM.

## Metadata-driven lookups

Attach routing hints or provenance data when you store entries and query them later:

```bash
# Merge metadata into an existing entry
curl -X PATCH \
    -H "Content-Type: application/json" \
    localhost:8080/entries/42/metadata \
    -d '{"metadata":{"intent":"faq","locale":"en-US"}}'

# List only entries tagged as FAQ for English
curl "localhost:8080/entries?metadata.intent=faq&metadata.locale=en-US"

# Apply the same filters to semantic search results
curl "localhost:8080/search?q=refunds&metadata.intent=faq"
```

Filters use string comparison across all metadata keys, which makes it easy to
tag responses with arbitrary values (model name, temperature, compliance flags,
etc.) and fetch only the subsets you care about before reusing cached answers.
