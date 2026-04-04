package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"
)

// config stored at ~/.waiboard
type Config struct {
	URL    string `json:"url"`
	User   string `json:"user"`
	ApiKey string `json:"api_key"`
}

func configPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".waiboard")
}

func loadConfig() (*Config, error) {
	data, err := os.ReadFile(configPath())
	if err != nil {
		return nil, fmt.Errorf("not logged in — run: waiboard login")
	}
	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("invalid config file")
	}
	return &cfg, nil
}

func saveConfig(cfg *Config) error {
	data, _ := json.MarshalIndent(cfg, "", "  ")
	return os.WriteFile(configPath(), data, 0600)
}

// api helper

func apiRequest(method, path string, body interface{}) ([]byte, error) {
	cfg, err := loadConfig()
	if err != nil {
		return nil, err
	}

	url := strings.TrimRight(cfg.URL, "/") + path

	var reqBody io.Reader
	if body != nil {
		data, _ := json.Marshal(body)
		reqBody = bytes.NewReader(data)
	}

	req, err := http.NewRequest(method, url, reqBody)
	if err != nil {
		return nil, err
	}
	req.Header.Set("X-Api-Key", cfg.ApiKey)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("connection failed: %v", err)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)

	if resp.StatusCode >= 400 {
		var errResp map[string]interface{}
		if json.Unmarshal(respBody, &errResp) == nil {
			if detail, ok := errResp["detail"]; ok {
				return nil, fmt.Errorf("%v", detail)
			}
		}
		return nil, fmt.Errorf("error: %s", resp.Status)
	}

	return respBody, nil
}

func main() {
	root := &cobra.Command{
		Use:   "waiboard",
		Short: "waiboard cli",
	}

	// --- login ---
	loginCmd := &cobra.Command{
		Use:   "login",
		Short: "save connection details",
		RunE: func(cmd *cobra.Command, args []string) error {
			url, _ := cmd.Flags().GetString("url")
			user, _ := cmd.Flags().GetString("user")
			key, _ := cmd.Flags().GetString("key")

			cfg := &Config{URL: url, User: user, ApiKey: key}
			if err := saveConfig(cfg); err != nil {
				return err
			}

			// verify connection
			_, err := apiRequest("GET", "/api/current-user", nil)
			if err != nil {
				os.Remove(configPath())
				return fmt.Errorf("login failed: %v", err)
			}

			fmt.Println("logged in")
			return nil
		},
	}
	loginCmd.Flags().String("url", "", "server url (e.g. https://board.example.com)")
	loginCmd.Flags().String("user", "", "username")
	loginCmd.Flags().String("key", "", "api key")
	loginCmd.MarkFlagRequired("url")
	loginCmd.MarkFlagRequired("user")
	loginCmd.MarkFlagRequired("key")

	// --- logout ---
	logoutCmd := &cobra.Command{
		Use:   "logout",
		Short: "remove saved credentials",
		RunE: func(cmd *cobra.Command, args []string) error {
			os.Remove(configPath())
			fmt.Println("logged out")
			return nil
		},
	}

	// --- whoami ---
	whoamiCmd := &cobra.Command{
		Use:   "whoami",
		Short: "show current user",
		RunE: func(cmd *cobra.Command, args []string) error {
			data, err := apiRequest("GET", "/api/current-user", nil)
			if err != nil {
				return err
			}
			var user map[string]interface{}
			json.Unmarshal(data, &user)
			fmt.Printf("%s", user["username"])
			if admin, ok := user["is_admin"].(bool); ok && admin {
				fmt.Print(" (admin)")
			}
			fmt.Println()
			return nil
		},
	}

	// --- projects ---
	projectsCmd := &cobra.Command{
		Use:   "projects",
		Short: "list your projects",
		RunE: func(cmd *cobra.Command, args []string) error {
			data, err := apiRequest("GET", "/api/projects", nil)
			if err != nil {
				return err
			}
			var projects []map[string]interface{}
			json.Unmarshal(data, &projects)

			if len(projects) == 0 {
				fmt.Println("no projects")
				return nil
			}

			w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
			fmt.Fprintln(w, "code\tname")
			fmt.Fprintln(w, "----\t----")
			for _, p := range projects {
				fmt.Fprintf(w, "%s\t%s\n", p["acronym"], p["name"])
			}
			w.Flush()
			return nil
		},
	}

	// --- tickets ---
	ticketsCmd := &cobra.Command{
		Use:   "tickets",
		Short: "list tickets",
		RunE: func(cmd *cobra.Command, args []string) error {
			project, _ := cmd.Flags().GetString("project")
			status, _ := cmd.Flags().GetString("status")
			mine, _ := cmd.Flags().GetBool("mine")

			// resolve project code to id
			path := "/api/tickets"
			if project != "" {
				projects, err := apiRequest("GET", "/api/projects", nil)
				if err != nil {
					return err
				}
				var projectList []map[string]interface{}
				json.Unmarshal(projects, &projectList)

				var projectID float64
				for _, p := range projectList {
					if strings.EqualFold(p["acronym"].(string), project) {
						projectID = p["id"].(float64)
						break
					}
				}
				if projectID == 0 {
					return fmt.Errorf("project '%s' not found", project)
				}
				path = fmt.Sprintf("/api/tickets?project_id=%.0f", projectID)
			}

			data, err := apiRequest("GET", path, nil)
			if err != nil {
				return err
			}
			var tickets []map[string]interface{}
			json.Unmarshal(data, &tickets)

			// filter by status
			if status != "" {
				var filtered []map[string]interface{}
				for _, t := range tickets {
					if strings.EqualFold(t["column"].(string), status) {
						filtered = append(filtered, t)
					}
				}
				tickets = filtered
			}

			// filter by mine
			if mine {
				cfg, _ := loadConfig()
				var filtered []map[string]interface{}
				for _, t := range tickets {
					if au, ok := t["assigned_user"].(map[string]interface{}); ok {
						if au["username"] == cfg.User {
							filtered = append(filtered, t)
						}
					}
				}
				tickets = filtered
			}

			if len(tickets) == 0 {
				fmt.Println("no tickets")
				return nil
			}

			w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
			fmt.Fprintln(w, "ticket\tstatus\tassigned\ttitle")
			fmt.Fprintln(w, "------\t------\t--------\t-----")
			for _, t := range tickets {
				ticket := fmt.Sprintf("%.0f", t["id"].(float64))
				if p, ok := t["project"].(map[string]interface{}); ok {
					if num, ok := t["ticket_number"].(float64); ok {
						ticket = fmt.Sprintf("%s-%.0f", p["acronym"], num)
					}
				}
				col := t["column"].(string)
				assigned := ""
				if au, ok := t["assigned_user"].(map[string]interface{}); ok {
					assigned = au["username"].(string)
				}
				title := t["title"].(string)
				if len(title) > 40 {
					title = title[:37] + "..."
				}
				fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", ticket, col, assigned, title)
			}
			w.Flush()
			return nil
		},
	}
	ticketsCmd.Flags().StringP("project", "p", "", "filter by project code")
	ticketsCmd.Flags().StringP("status", "s", "", "filter by status (todo, inprogress, testing, done)")
	ticketsCmd.Flags().BoolP("mine", "m", false, "show only tickets assigned to you")

	// --- ticket (detail) ---
	ticketCmd := &cobra.Command{
		Use:   "ticket [id]",
		Short: "show ticket details",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			// fetch all tickets and find by id (no single-ticket endpoint)
			data, err := apiRequest("GET", "/api/tickets", nil)
			if err != nil {
				return err
			}
			var tickets []map[string]interface{}
			json.Unmarshal(data, &tickets)

			for _, t := range tickets {
				id := fmt.Sprintf("%.0f", t["id"].(float64))
				if id == args[0] {
					ticket := id
					if p, ok := t["project"].(map[string]interface{}); ok {
						if num, ok := t["ticket_number"].(float64); ok {
							ticket = fmt.Sprintf("%s-%.0f", p["acronym"], num)
						}
						fmt.Printf("ticket:      %s\n", ticket)
						fmt.Printf("project:     %s (%s)\n", p["name"], p["acronym"])
					} else {
						fmt.Printf("ticket:      %s\n", ticket)
					}
					fmt.Printf("title:       %s\n", t["title"])
					fmt.Printf("status:      %s\n", t["column"])
					if au, ok := t["assigned_user"].(map[string]interface{}); ok {
						fmt.Printf("assigned:    %s\n", au["username"])
					} else {
						fmt.Printf("assigned:    unassigned\n")
					}
					desc := ""
					if d, ok := t["description"].(string); ok {
						desc = d
					}
					if desc != "" {
						fmt.Printf("description: %s\n", desc)
					}
					return nil
				}
			}
			return fmt.Errorf("ticket %s not found", args[0])
		},
	}

	// --- move ---
	moveCmd := &cobra.Command{
		Use:   "move [id] [status]",
		Short: "update ticket status (todo, inprogress, testing, done)",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := strconv.Atoi(args[0])
			if err != nil {
				return fmt.Errorf("invalid ticket id")
			}
			column := strings.ToLower(args[1])
			valid := map[string]bool{"todo": true, "inprogress": true, "testing": true, "done": true}
			if !valid[column] {
				return fmt.Errorf("invalid status: %s (use: todo, inprogress, testing, done)", column)
			}

			path := fmt.Sprintf("/api/tickets/%d", id)
			_, err = apiRequest("PATCH", path, map[string]string{"column": column})
			if err != nil {
				return err
			}
			fmt.Printf("ticket %d → %s\n", id, column)
			return nil
		},
	}

	// --- new ---
	newCmd := &cobra.Command{
		Use:   "new [title]",
		Short: "create a new ticket",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			project, _ := cmd.Flags().GetString("project")
			desc, _ := cmd.Flags().GetString("desc")

			if project == "" {
				return fmt.Errorf("--project is required")
			}

			// resolve project code to id
			projects, err := apiRequest("GET", "/api/projects", nil)
			if err != nil {
				return err
			}
			var projectList []map[string]interface{}
			json.Unmarshal(projects, &projectList)

			var projectID float64
			for _, p := range projectList {
				if strings.EqualFold(p["acronym"].(string), project) {
					projectID = p["id"].(float64)
					break
				}
			}
			if projectID == 0 {
				return fmt.Errorf("project '%s' not found", project)
			}

			path := fmt.Sprintf("/api/tickets?project_id=%.0f", projectID)
			body := map[string]string{"title": args[0]}
			if desc != "" {
				body["description"] = desc
			}

			data, err := apiRequest("POST", path, body)
			if err != nil {
				return err
			}

			var ticket map[string]interface{}
			json.Unmarshal(data, &ticket)
			fmt.Printf("created ticket %.0f: %s\n", ticket["id"].(float64), ticket["title"])
			return nil
		},
	}
	newCmd.Flags().StringP("project", "p", "", "project code (required)")
	newCmd.Flags().StringP("desc", "d", "", "description")
	newCmd.MarkFlagRequired("project")

	root.AddCommand(loginCmd, logoutCmd, whoamiCmd, projectsCmd, ticketsCmd, ticketCmd, moveCmd, newCmd)
	root.Execute()
}
