package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
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

func currentUsername() (string, error) {
	data, err := apiRequest("GET", "/api/current-user", nil)
	if err != nil {
		return "", err
	}

	var user map[string]interface{}
	json.Unmarshal(data, &user)

	username, ok := user["username"].(string)
	if !ok || username == "" {
		return "", fmt.Errorf("could not determine current user")
	}

	return username, nil
}

func publicTicketID(t map[string]interface{}) string {
	if p, ok := t["project"].(map[string]interface{}); ok {
		if acronym, ok := p["acronym"].(string); ok {
			if num, ok := t["ticket_number"].(float64); ok {
				return fmt.Sprintf("%s-%.0f", acronym, num)
			}
		}
	}
	return ""
}

// resolveTicket finds a ticket by code ("TST-1")
// returns the internal id and the public display name
func resolveTicket(arg string) (int, string, error) {
	data, err := apiRequest("GET", "/api/tickets", nil)
	if err != nil {
		return 0, "", err
	}
	var tickets []map[string]interface{}
	json.Unmarshal(data, &tickets)

	for _, t := range tickets {
		dbID := int(t["id"].(float64))
		display := publicTicketID(t)

		if strings.EqualFold(display, arg) {
			return dbID, display, nil
		}
	}
	return 0, "", fmt.Errorf("ticket %s not found", arg)
}

func ticketSortParts(t map[string]interface{}) (string, int, int) {
	project := ""
	if p, ok := t["project"].(map[string]interface{}); ok {
		if acronym, ok := p["acronym"].(string); ok {
			project = acronym
		}
	}

	number := 0
	if n, ok := t["ticket_number"].(float64); ok {
		number = int(n)
	}

	id := 0
	if n, ok := t["id"].(float64); ok {
		id = int(n)
	}

	return project, number, id
}

func getTicketByID(dbID int) (map[string]interface{}, error) {
	data, err := apiRequest("GET", "/api/tickets", nil)
	if err != nil {
		return nil, err
	}

	var tickets []map[string]interface{}
	json.Unmarshal(data, &tickets)

	for _, t := range tickets {
		if int(t["id"].(float64)) == dbID {
			return t, nil
		}
	}

	return nil, fmt.Errorf("ticket not found")
}

func resolveProjectUserID(projectID int, username string) (int, string, error) {
	path := fmt.Sprintf("/api/projects/%d/users", projectID)
	data, err := apiRequest("GET", path, nil)
	if err != nil {
		return 0, "", err
	}

	var users []map[string]interface{}
	json.Unmarshal(data, &users)

	for _, u := range users {
		if strings.EqualFold(u["username"].(string), username) {
			return int(u["id"].(float64)), u["username"].(string), nil
		}
	}

	return 0, "", fmt.Errorf("user '%s' not found in this project", username)
}

func resolveProjectID(arg string) (int, string, string, error) {
	data, err := apiRequest("GET", "/api/projects", nil)
	if err != nil {
		return 0, "", "", err
	}

	var projects []map[string]interface{}
	json.Unmarshal(data, &projects)

	for _, p := range projects {
		id := int(p["id"].(float64))
		name := p["name"].(string)
		acronym := p["acronym"].(string)

		if strings.EqualFold(acronym, arg) || strings.EqualFold(name, arg) {
			return id, name, acronym, nil
		}
	}

	return 0, "", "", fmt.Errorf("project '%s' not found", arg)
}

func isValidStatus(column string) bool {
	valid := map[string]bool{"todo": true, "inprogress": true, "testing": true, "done": true}
	return valid[column]
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
			key, _ := cmd.Flags().GetString("key")

			cfg := &Config{URL: url, ApiKey: key}
			if err := saveConfig(cfg); err != nil {
				return err
			}

			// verify connection
			username, err := currentUsername()
			if err != nil {
				os.Remove(configPath())
				return fmt.Errorf("login failed: %v", err)
			}

			cfg.User = username
			if err := saveConfig(cfg); err != nil {
				return err
			}

			fmt.Println("logged in")
			return nil
		},
	}
	loginCmd.Flags().String("url", "", "server url (e.g. https://board.example.com)")
	loginCmd.Flags().String("key", "", "api key")
	loginCmd.MarkFlagRequired("url")
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
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			project, _ := cmd.Flags().GetString("project")
			status, _ := cmd.Flags().GetString("status")
			mine, _ := cmd.Flags().GetBool("mine")
			showDesc, _ := cmd.Flags().GetBool("desc")

			// resolve project code to id
			path := "/api/tickets"
			if project != "" {
				projectID, _, _, err := resolveProjectID(project)
				if err != nil {
					return err
				}
				path = fmt.Sprintf("/api/tickets?project_id=%d", projectID)
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
				username, err := currentUsername()
				if err != nil {
					return err
				}
				var filtered []map[string]interface{}
				for _, t := range tickets {
					if au, ok := t["assigned_user"].(map[string]interface{}); ok {
						if au["username"] == username {
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

			sort.Slice(tickets, func(i, j int) bool {
				projectI, numberI, idI := ticketSortParts(tickets[i])
				projectJ, numberJ, idJ := ticketSortParts(tickets[j])

				if projectI != projectJ {
					return projectI < projectJ
				}
				if numberI != numberJ {
					return numberI < numberJ
				}
				return idI < idJ
			})

			w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
			if showDesc && mine {
				fmt.Fprintln(w, "ticket\tstatus\ttitle\tdescription")
				fmt.Fprintln(w, "------\t------\t-----\t-----------")
			} else if showDesc {
				fmt.Fprintln(w, "ticket\tstatus\tassigned\ttitle\tdescription")
				fmt.Fprintln(w, "------\t------\t--------\t-----\t-----------")
			} else if mine {
				fmt.Fprintln(w, "ticket\tstatus\ttitle")
				fmt.Fprintln(w, "------\t------\t-----")
			} else {
				fmt.Fprintln(w, "ticket\tstatus\tassigned\ttitle")
				fmt.Fprintln(w, "------\t------\t--------\t-----")
			}
			for _, t := range tickets {
				ticket := publicTicketID(t)
				if ticket == "" {
					ticket = "unknown"
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
				description := ""
				if showDesc {
					if d, ok := t["description"].(string); ok {
						description = d
					}
					description = strings.ReplaceAll(description, "\n", " ")
					description = strings.TrimSpace(description)
					if len(description) > 60 {
						description = description[:57] + "..."
					}
				}
				if showDesc && mine {
					fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", ticket, col, title, description)
				} else if showDesc {
					fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n", ticket, col, assigned, title, description)
				} else if mine {
					fmt.Fprintf(w, "%s\t%s\t%s\n", ticket, col, title)
				} else {
					fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", ticket, col, assigned, title)
				}
			}
			w.Flush()
			return nil
		},
	}
	ticketsCmd.Flags().StringP("project", "p", "", "filter by project code or name")
	ticketsCmd.Flags().StringP("status", "s", "", "filter by status (todo, inprogress, testing, done)")
	ticketsCmd.Flags().BoolP("mine", "m", false, "show only tickets assigned to you")
	ticketsCmd.Flags().Bool("desc", false, "show ticket descriptions")

	// --- ticket (detail) ---
	ticketCmd := &cobra.Command{
		Use:   "ticket [id]",
		Short: "show ticket details",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			dbID, _, err := resolveTicket(args[0])
			if err != nil {
				return err
			}

			t, err := getTicketByID(dbID)
			if err != nil {
				return err
			}

			display := publicTicketID(t)
			if p, ok := t["project"].(map[string]interface{}); ok {
				fmt.Printf("ticket:      %s\n", display)
				fmt.Printf("project:     %s (%s)\n", p["name"], p["acronym"])
			} else {
				fmt.Printf("ticket:      %s\n", display)
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
		},
	}

	// --- assign ---
	assignCmd := &cobra.Command{
		Use:   "assign [id] [username]",
		Short: "assign a ticket to a user",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			dbID, display, err := resolveTicket(args[0])
			if err != nil {
				return err
			}

			ticket, err := getTicketByID(dbID)
			if err != nil {
				return err
			}

			project, ok := ticket["project"].(map[string]interface{})
			if !ok {
				return fmt.Errorf("ticket project not found")
			}

			projectID := int(project["id"].(float64))
			userID, username, err := resolveProjectUserID(projectID, args[1])
			if err != nil {
				return err
			}

			path := fmt.Sprintf("/api/tickets/%d", dbID)
			_, err = apiRequest("PATCH", path, map[string]int{"assigned_user_id": userID})
			if err != nil {
				return err
			}

			fmt.Printf("%s assigned to %s\n", display, username)
			return nil
		},
	}

	// --- unassign ---
	unassignCmd := &cobra.Command{
		Use:   "unassign [id]",
		Short: "remove ticket assignment",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			dbID, display, err := resolveTicket(args[0])
			if err != nil {
				return err
			}

			path := fmt.Sprintf("/api/tickets/%d", dbID)
			_, err = apiRequest("PATCH", path, map[string]int{"assigned_user_id": 0})
			if err != nil {
				return err
			}

			fmt.Printf("%s unassigned\n", display)
			return nil
		},
	}

	// --- edit ---
	editCmd := &cobra.Command{
		Use:   "edit [id]",
		Short: "edit an existing ticket",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			dbID, display, err := resolveTicket(args[0])
			if err != nil {
				return err
			}

			title, _ := cmd.Flags().GetString("title")
			desc, _ := cmd.Flags().GetString("desc")
			clearDesc, _ := cmd.Flags().GetBool("clear-desc")

			body := map[string]interface{}{}

			if cmd.Flags().Changed("title") {
				body["title"] = title
			}
			if cmd.Flags().Changed("desc") {
				body["description"] = desc
			}
			if clearDesc {
				body["description"] = ""
			}

			if len(body) == 0 {
				return fmt.Errorf("nothing to edit")
			}

			path := fmt.Sprintf("/api/tickets/%d", dbID)
			_, err = apiRequest("PATCH", path, body)
			if err != nil {
				return err
			}

			fmt.Printf("updated %s\n", display)
			return nil
		},
	}
	editCmd.Flags().String("title", "", "new ticket title")
	editCmd.Flags().String("desc", "", "new ticket description")
	editCmd.Flags().Bool("clear-desc", false, "clear ticket description")

	// --- delete ---
	deleteCmd := &cobra.Command{
		Use:   "delete [id]",
		Short: "delete a ticket",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			dbID, display, err := resolveTicket(args[0])
			if err != nil {
				return err
			}

			path := fmt.Sprintf("/api/tickets/%d", dbID)
			_, err = apiRequest("DELETE", path, nil)
			if err != nil {
				return err
			}

			fmt.Printf("deleted %s\n", display)
			return nil
		},
	}

	// --- move ---
	moveCmd := &cobra.Command{
		Use:   "move [id] [status]",
		Short: "update ticket status",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			dbID, display, err := resolveTicket(args[0])
			if err != nil {
				return err
			}
			column := strings.ToLower(args[1])
			if !isValidStatus(column) {
				return fmt.Errorf("invalid status: %s (use: todo, inprogress, testing, done)", column)
			}

			path := fmt.Sprintf("/api/tickets/%d", dbID)
			_, err = apiRequest("PATCH", path, map[string]string{"column": column})
			if err != nil {
				return err
			}
			fmt.Printf("%s → %s\n", display, column)
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

			projectID, _, _, err := resolveProjectID(project)
			if err != nil {
				return err
			}

			path := fmt.Sprintf("/api/tickets?project_id=%d", projectID)
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
			display := publicTicketID(ticket)
			if display == "" {
				return fmt.Errorf("ticket created, but public ticket id was not returned")
			}
			fmt.Printf("created %s: %s\n", display, ticket["title"])
			return nil
		},
	}
	newCmd.Flags().StringP("project", "p", "", "project code or name (required)")
	newCmd.Flags().StringP("desc", "d", "", "description")
	newCmd.MarkFlagRequired("project")

	root.AddCommand(loginCmd, logoutCmd, whoamiCmd, projectsCmd, ticketsCmd, ticketCmd, assignCmd, unassignCmd, editCmd, deleteCmd, moveCmd, newCmd)
	root.Execute()
}
