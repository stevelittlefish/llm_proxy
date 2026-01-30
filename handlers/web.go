package handlers

import (
	"fmt"
	"html/template"
	"log"
	"net/http"
	"strconv"

	"llm_proxy/database"
)

const pageSize = 25

// WebHandler handles the web UI for viewing logs
type WebHandler struct {
	db *database.DB
}

// NewWebHandler creates a new web handler
func NewWebHandler(db *database.DB) *WebHandler {
	return &WebHandler{db: db}
}

// truncateString truncates a string to a maximum length
func truncateString(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}

// formatBytes formats bytes in a human-readable way
func formatBytes(size int) string {
	if size < 1024 {
		return fmt.Sprintf("%d B", size)
	} else if size < 1024*1024 {
		return fmt.Sprintf("%.2f KB", float64(size)/1024)
	} else {
		return fmt.Sprintf("%.2f MB", float64(size)/(1024*1024))
	}
}

// IndexHandler serves the index page with paginated list
func (h *WebHandler) IndexHandler(w http.ResponseWriter, r *http.Request) {
	// Get page number from query params
	page := 1
	if pageStr := r.URL.Query().Get("page"); pageStr != "" {
		if p, err := strconv.Atoi(pageStr); err == nil && p > 0 {
			page = p
		}
	}

	offset := (page - 1) * pageSize

	// Get total count for pagination
	total, err := h.db.GetTotalCount()
	if err != nil {
		log.Printf("Error getting total count: %v", err)
		http.Error(w, "Database error", http.StatusInternalServerError)
		return
	}

	// Get entries
	entries, err := h.db.GetRecentEntries(pageSize, offset)
	if err != nil {
		log.Printf("Error getting entries: %v", err)
		http.Error(w, "Database error", http.StatusInternalServerError)
		return
	}

	totalPages := int((total + int64(pageSize) - 1) / int64(pageSize))

	// Prepare template data
	data := struct {
		Entries     []database.LogEntry
		CurrentPage int
		TotalPages  int
		TotalCount  int64
		HasPrev     bool
		HasNext     bool
		PrevPage    int
		NextPage    int
	}{
		Entries:     entries,
		CurrentPage: page,
		TotalPages:  totalPages,
		TotalCount:  total,
		HasPrev:     page > 1,
		HasNext:     page < totalPages,
		PrevPage:    page - 1,
		NextPage:    page + 1,
	}

	// Create template with functions
	tmpl := template.Must(template.New("index").Funcs(template.FuncMap{
		"truncate": truncateString,
	}).Parse(indexTemplate))

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := tmpl.Execute(w, data); err != nil {
		log.Printf("Error executing template: %v", err)
		http.Error(w, "Template error", http.StatusInternalServerError)
		return
	}
}

// DetailsHandler serves the details page for a specific request
func (h *WebHandler) DetailsHandler(w http.ResponseWriter, r *http.Request) {
	// Get ID from query params
	idStr := r.URL.Query().Get("id")
	if idStr == "" {
		http.Error(w, "Missing ID parameter", http.StatusBadRequest)
		return
	}

	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		http.Error(w, "Invalid ID parameter", http.StatusBadRequest)
		return
	}

	// Get entry
	entry, err := h.db.GetEntryByID(id)
	if err != nil {
		log.Printf("Error getting entry: %v", err)
		http.Error(w, "Database error", http.StatusInternalServerError)
		return
	}

	if entry == nil {
		http.NotFound(w, r)
		return
	}

	// Create template with functions
	tmpl := template.Must(template.New("details").Funcs(template.FuncMap{
		"formatBytes": formatBytes,
	}).Parse(detailsTemplate))

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := tmpl.Execute(w, entry); err != nil {
		log.Printf("Error executing template: %v", err)
		http.Error(w, "Template error", http.StatusInternalServerError)
		return
	}
}

const indexTemplate = `<!DOCTYPE html>
<html lang="en">
<head>
    <meta charset="UTF-8">
    <meta name="viewport" content="width=device-width, initial-scale=1.0">
    <title>LLM Proxy - Request Log</title>
    <style>
        * {
            margin: 0;
            padding: 0;
            box-sizing: border-box;
        }
        body {
            font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, "Helvetica Neue", Arial, sans-serif;
            background: #f5f5f5;
            color: #333;
            line-height: 1.6;
        }
        .container {
            max-width: 1400px;
            margin: 0 auto;
            padding: 20px;
        }
        header {
            background: white;
            padding: 20px;
            margin-bottom: 20px;
            border-radius: 8px;
            box-shadow: 0 2px 4px rgba(0,0,0,0.1);
        }
        h1 {
            color: #2c3e50;
            margin-bottom: 10px;
        }
        .stats {
            color: #7f8c8d;
            font-size: 14px;
        }
        .table-container {
            background: white;
            border-radius: 8px;
            box-shadow: 0 2px 4px rgba(0,0,0,0.1);
            overflow: hidden;
        }
        table {
            width: 100%;
            border-collapse: collapse;
        }
        thead {
            background: #34495e;
            color: white;
        }
        th {
            padding: 12px;
            text-align: left;
            font-weight: 600;
            font-size: 14px;
        }
        td {
            padding: 12px;
            border-bottom: 1px solid #ecf0f1;
            font-size: 13px;
        }
        tr:hover {
            background: #f8f9fa;
        }
        .timestamp {
            font-family: "Courier New", monospace;
            color: #7f8c8d;
            white-space: nowrap;
        }
        .endpoint {
            font-weight: 500;
            color: #2980b9;
        }
        .model {
            color: #27ae60;
        }
        .status-ok {
            color: #27ae60;
            font-weight: 600;
        }
        .status-error {
            color: #e74c3c;
            font-weight: 600;
        }
        .latency {
            color: #8e44ad;
            font-family: "Courier New", monospace;
        }
        .stream-badge {
            display: inline-block;
            padding: 2px 8px;
            border-radius: 4px;
            font-size: 11px;
            font-weight: 600;
            background: #3498db;
            color: white;
        }
        .error-badge {
            display: inline-block;
            padding: 2px 8px;
            border-radius: 4px;
            font-size: 11px;
            font-weight: 600;
            background: #e74c3c;
            color: white;
        }
        .truncated {
            color: #95a5a6;
            font-family: "Courier New", monospace;
            font-size: 12px;
        }
        .pagination {
            display: flex;
            justify-content: center;
            align-items: center;
            gap: 10px;
            margin-top: 20px;
            padding: 20px;
        }
        .pagination a, .pagination span {
            padding: 8px 16px;
            background: white;
            border-radius: 4px;
            text-decoration: none;
            color: #2c3e50;
            box-shadow: 0 2px 4px rgba(0,0,0,0.1);
        }
        .pagination a:hover {
            background: #3498db;
            color: white;
        }
        .pagination .current {
            background: #34495e;
            color: white;
            font-weight: 600;
        }
        .pagination .disabled {
            opacity: 0.5;
            pointer-events: none;
        }
        a {
            color: #3498db;
            text-decoration: none;
        }
        a:hover {
            text-decoration: underline;
        }
    </style>
</head>
<body>
    <div class="container">
        <header>
            <h1>🔄 LLM Proxy Request Log</h1>
            <div class="stats">Total Requests: {{.TotalCount}} | Page {{.CurrentPage}} of {{.TotalPages}}</div>
        </header>

        <div class="table-container">
            <table>
                <thead>
                    <tr>
                        <th>ID</th>
                        <th>Timestamp</th>
                        <th>Endpoint</th>
                        <th>Model</th>
                        <th>Status</th>
                        <th>Latency</th>
                        <th>Flags</th>
                        <th>Preview</th>
                    </tr>
                </thead>
                <tbody>
                    {{range .Entries}}
                    <tr>
                        <td><a href="/logs/details?id={{.ID}}">#{{.ID}}</a></td>
                        <td class="timestamp">{{.Timestamp.Format "2006-01-02 15:04:05"}}</td>
                        <td class="endpoint">{{.Endpoint}}</td>
                        <td class="model">{{.Model}}</td>
                        <td class="{{if eq .StatusCode 200}}status-ok{{else}}status-error{{end}}">{{.StatusCode}}</td>
                        <td class="latency">{{.LatencyMs}}ms</td>
                        <td>
                            {{if .Stream}}<span class="stream-badge">STREAM</span>{{end}}
                            {{if .Error}}<span class="error-badge">ERROR</span>{{end}}
                        </td>
                        <td class="truncated">{{truncate .Prompt 80}}</td>
                    </tr>
                    {{else}}
                    <tr>
                        <td colspan="8" style="text-align: center; padding: 40px; color: #95a5a6;">
                            No requests logged yet
                        </td>
                    </tr>
                    {{end}}
                </tbody>
            </table>
        </div>

        {{if gt .TotalPages 1}}
        <div class="pagination">
            {{if .HasPrev}}
                <a href="?page={{.PrevPage}}">← Previous</a>
            {{else}}
                <span class="disabled">← Previous</span>
            {{end}}
            
            <span class="current">Page {{.CurrentPage}} of {{.TotalPages}}</span>
            
            {{if .HasNext}}
                <a href="?page={{.NextPage}}">Next →</a>
            {{else}}
                <span class="disabled">Next →</span>
            {{end}}
        </div>
        {{end}}
    </div>
</body>
</html>`

const detailsTemplate = `<!DOCTYPE html>
<html lang="en">
<head>
    <meta charset="UTF-8">
    <meta name="viewport" content="width=device-width, initial-scale=1.0">
    <title>Request #{{.ID}} - LLM Proxy</title>
    <style>
        * {
            margin: 0;
            padding: 0;
            box-sizing: border-box;
        }
        body {
            font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, "Helvetica Neue", Arial, sans-serif;
            background: #f5f5f5;
            color: #333;
            line-height: 1.6;
        }
        .container {
            max-width: 1200px;
            margin: 0 auto;
            padding: 20px;
        }
        header {
            background: white;
            padding: 20px;
            margin-bottom: 20px;
            border-radius: 8px;
            box-shadow: 0 2px 4px rgba(0,0,0,0.1);
        }
        h1 {
            color: #2c3e50;
            margin-bottom: 10px;
        }
        .back-link {
            display: inline-block;
            margin-top: 10px;
            color: #3498db;
            text-decoration: none;
        }
        .back-link:hover {
            text-decoration: underline;
        }
        .section {
            background: white;
            padding: 20px;
            margin-bottom: 20px;
            border-radius: 8px;
            box-shadow: 0 2px 4px rgba(0,0,0,0.1);
        }
        h2 {
            color: #34495e;
            margin-bottom: 15px;
            padding-bottom: 10px;
            border-bottom: 2px solid #ecf0f1;
        }
        .info-grid {
            display: grid;
            grid-template-columns: repeat(auto-fit, minmax(250px, 1fr));
            gap: 15px;
            margin-bottom: 20px;
        }
        .info-item {
            padding: 10px;
            background: #f8f9fa;
            border-radius: 4px;
        }
        .info-label {
            font-weight: 600;
            color: #7f8c8d;
            font-size: 12px;
            text-transform: uppercase;
            margin-bottom: 5px;
        }
        .info-value {
            color: #2c3e50;
            font-size: 14px;
            word-break: break-word;
        }
        .code-block {
            background: #2c3e50;
            color: #ecf0f1;
            padding: 15px;
            border-radius: 4px;
            overflow-x: auto;
            font-family: "Courier New", monospace;
            font-size: 13px;
            line-height: 1.5;
            white-space: pre-wrap;
            word-wrap: break-word;
            max-height: 500px;
            overflow-y: auto;
        }
        .status-ok {
            color: #27ae60;
            font-weight: 600;
        }
        .status-error {
            color: #e74c3c;
            font-weight: 600;
        }
        .stream-badge {
            display: inline-block;
            padding: 4px 12px;
            border-radius: 4px;
            font-size: 12px;
            font-weight: 600;
            background: #3498db;
            color: white;
        }
        .error-box {
            background: #fee;
            border-left: 4px solid #e74c3c;
            padding: 15px;
            border-radius: 4px;
            color: #c0392b;
            margin-top: 10px;
        }
        .collapsible {
            cursor: pointer;
            user-select: none;
        }
        .collapsible::before {
            content: "▼ ";
            font-size: 10px;
        }
        .collapsible.collapsed::before {
            content: "▶ ";
        }
        .collapsible-content {
            margin-top: 10px;
        }
        .collapsible-content.hidden {
            display: none;
        }
        .size-info {
            color: #95a5a6;
            font-size: 12px;
            margin-bottom: 5px;
        }
    </style>
    <script>
        function toggleCollapse(id) {
            const header = document.getElementById('header-' + id);
            const content = document.getElementById('content-' + id);
            header.classList.toggle('collapsed');
            content.classList.toggle('hidden');
        }
        
        function formatJSON(jsonStr) {
            if (!jsonStr) return '';
            try {
                return JSON.stringify(JSON.parse(jsonStr), null, 2);
            } catch (e) {
                return jsonStr;
            }
        }
        
        window.addEventListener('DOMContentLoaded', function() {
            // Format all JSON code blocks
            document.querySelectorAll('.json-content').forEach(function(el) {
                el.textContent = formatJSON(el.textContent);
            });
        });
    </script>
</head>
<body>
    <div class="container">
        <header>
            <h1>📋 Request #{{.ID}}</h1>
            <a href="/logs" class="back-link">← Back to list</a>
        </header>

        <div class="section">
            <h2>Overview</h2>
            <div class="info-grid">
                <div class="info-item">
                    <div class="info-label">Timestamp</div>
                    <div class="info-value">{{.Timestamp.Format "2006-01-02 15:04:05 MST"}}</div>
                </div>
                <div class="info-item">
                    <div class="info-label">Endpoint</div>
                    <div class="info-value">{{.Endpoint}}</div>
                </div>
                <div class="info-item">
                    <div class="info-label">Method</div>
                    <div class="info-value">{{.Method}}</div>
                </div>
                <div class="info-item">
                    <div class="info-label">Model</div>
                    <div class="info-value">{{.Model}}</div>
                </div>
                <div class="info-item">
                    <div class="info-label">Status Code</div>
                    <div class="info-value {{if eq .StatusCode 200}}status-ok{{else}}status-error{{end}}">{{.StatusCode}}</div>
                </div>
                <div class="info-item">
                    <div class="info-label">Latency</div>
                    <div class="info-value">{{.LatencyMs}} ms</div>
                </div>
                <div class="info-item">
                    <div class="info-label">Backend Type</div>
                    <div class="info-value">{{.BackendType}}</div>
                </div>
                <div class="info-item">
                    <div class="info-label">Stream</div>
                    <div class="info-value">{{if .Stream}}<span class="stream-badge">YES</span>{{else}}No{{end}}</div>
                </div>
            </div>

            {{if .Error}}
            <div class="error-box">
                <strong>Error:</strong> {{.Error}}
            </div>
            {{end}}
        </div>

        <div class="section">
            <h2>URLs</h2>
            <div class="info-grid">
                <div class="info-item">
                    <div class="info-label">Frontend URL</div>
                    <div class="info-value">{{.FrontendURL}}</div>
                </div>
                <div class="info-item">
                    <div class="info-label">Backend URL</div>
                    <div class="info-value">{{.BackendURL}}</div>
                </div>
            </div>
        </div>

        <div class="section">
            <h2>Prompt & Response</h2>
            <div class="info-item" style="margin-bottom: 15px;">
                <div class="info-label">Prompt</div>
                <div class="info-value">{{.Prompt}}</div>
            </div>
            <div class="info-item">
                <div class="info-label">Response</div>
                <div class="info-value">{{.Response}}</div>
            </div>
        </div>

        {{if .FrontendRequest}}
        <div class="section">
            <h2 class="collapsible" id="header-fe-req" onclick="toggleCollapse('fe-req')">Frontend Request</h2>
            <div class="collapsible-content" id="content-fe-req">
                <div class="size-info">Size: {{formatBytes (len .FrontendRequest)}}</div>
                <pre class="code-block json-content">{{.FrontendRequest}}</pre>
            </div>
        </div>
        {{end}}

        {{if .BackendRequest}}
        <div class="section">
            <h2 class="collapsible" id="header-be-req" onclick="toggleCollapse('be-req')">Backend Request</h2>
            <div class="collapsible-content" id="content-be-req">
                <div class="size-info">Size: {{formatBytes (len .BackendRequest)}}</div>
                <pre class="code-block json-content">{{.BackendRequest}}</pre>
            </div>
        </div>
        {{end}}

        {{if .FrontendResponse}}
        <div class="section">
            <h2 class="collapsible" id="header-fe-res" onclick="toggleCollapse('fe-res')">Frontend Response</h2>
            <div class="collapsible-content" id="content-fe-res">
                <div class="size-info">Size: {{formatBytes (len .FrontendResponse)}}</div>
                <pre class="code-block json-content">{{.FrontendResponse}}</pre>
            </div>
        </div>
        {{end}}

        {{if .BackendResponse}}
        <div class="section">
            <h2 class="collapsible" id="header-be-res" onclick="toggleCollapse('be-res')">Backend Response</h2>
            <div class="collapsible-content" id="content-be-res">
                <div class="size-info">Size: {{formatBytes (len .BackendResponse)}}</div>
                <pre class="code-block json-content">{{.BackendResponse}}</pre>
            </div>
        </div>
        {{end}}

        <div style="text-align: center; padding: 20px;">
            <a href="/logs" class="back-link">← Back to list</a>
        </div>
    </div>
</body>
</html>`
