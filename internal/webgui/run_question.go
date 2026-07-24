package webgui

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"supercli/internal/tools"
	"supercli/internal/tools/sandbox"
)

var errQuestionNotFound = errors.New("question is no longer active")

type questionOptionWire struct {
	Label       string `json:"label"`
	Description string `json:"description,omitempty"`
	Preview     string `json:"preview,omitempty"`
	Image       string `json:"image,omitempty"`
	ImagePrompt string `json:"image_prompt,omitempty"`
}

type questionWire struct {
	ID          string               `json:"id"`
	Question    string               `json:"question"`
	Header      string               `json:"header,omitempty"`
	Options     []questionOptionWire `json:"options"`
	MultiSelect bool                 `json:"multi_select"`
	AllowCustom bool                 `json:"allow_custom"`
}

func (e *Engine) registerQuestion(req tools.AskRequest) questionWire {
	e.questionMu.Lock()
	e.questions[req.ID] = req
	e.questionMu.Unlock()
	out := questionWire{
		ID: req.ID, Question: req.Question, Header: req.Header,
		MultiSelect: req.MultiSelect, AllowCustom: req.AllowCustom,
		Options: make([]questionOptionWire, 0, len(req.Options)),
	}
	for i, option := range req.Options {
		imageURL := ""
		if strings.TrimSpace(option.Image) != "" {
			imageURL = "/api/question/image?id=" + req.ID + "&option=" + strconv.Itoa(i)
		}
		out.Options = append(out.Options, questionOptionWire{
			Label: option.Label, Description: option.Description, Preview: option.Preview,
			Image: imageURL, ImagePrompt: option.ImagePrompt,
		})
	}
	return out
}

func (e *Engine) cancelQuestion(id string) {
	e.questionMu.Lock()
	req, ok := e.questions[id]
	delete(e.questions, id)
	e.questionMu.Unlock()
	if ok {
		select {
		case req.Respond <- tools.AskAnswer{Cancelled: true}:
		default:
		}
	}
}

func (e *Engine) answerQuestion(id string, answer tools.AskAnswer) error {
	e.questionMu.Lock()
	req, ok := e.questions[id]
	e.questionMu.Unlock()
	if !ok {
		return errQuestionNotFound
	}
	allowed := make(map[string]bool, len(req.Options))
	for _, option := range req.Options {
		allowed[option.Label] = true
	}
	for _, selected := range answer.Selected {
		if !allowed[selected] {
			return fmt.Errorf("unknown option %q", selected)
		}
	}
	if !req.MultiSelect && len(answer.Selected) > 1 {
		return errors.New("question accepts one option")
	}
	answer.MultiSelect = req.MultiSelect
	answer.Custom = strings.TrimSpace(answer.Custom)
	if !answer.Cancelled && len(answer.Selected) == 0 && answer.Custom == "" {
		return errors.New("select an option or enter your own answer")
	}
	e.questionMu.Lock()
	current, stillActive := e.questions[id]
	if stillActive && current.ID == req.ID {
		delete(e.questions, id)
	}
	e.questionMu.Unlock()
	if !stillActive {
		return errQuestionNotFound
	}
	select {
	case req.Respond <- answer:
		return nil
	case <-time.After(time.Second):
		return errQuestionNotFound
	}
}

func (s *Server) handleQuestionAnswer(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var body struct {
		ID        string   `json:"id"`
		Selected  []string `json:"selected"`
		Custom    string   `json:"custom"`
		Cancelled bool     `json:"cancelled"`
	}
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&body); err != nil {
		http.Error(w, "bad request: "+err.Error(), http.StatusBadRequest)
		return
	}
	err := s.eng.answerQuestion(strings.TrimSpace(body.ID), tools.AskAnswer{Selected: body.Selected, Custom: body.Custom, Cancelled: body.Cancelled})
	if err != nil {
		status := http.StatusBadRequest
		if errors.Is(err, errQuestionNotFound) {
			status = http.StatusGone
		}
		http.Error(w, err.Error(), status)
		return
	}
	writeJSON(w, map[string]bool{"ok": true})
}

func (s *Server) handleQuestionImage(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(r.URL.Query().Get("id"))
	idx, err := strconv.Atoi(r.URL.Query().Get("option"))
	if err != nil {
		http.Error(w, "bad option", http.StatusBadRequest)
		return
	}
	s.eng.questionMu.Lock()
	req, ok := s.eng.questions[id]
	s.eng.questionMu.Unlock()
	if !ok || idx < 0 || idx >= len(req.Options) {
		http.Error(w, errQuestionNotFound.Error(), http.StatusGone)
		return
	}
	path, err := sandbox.ResolveSafe(s.eng.Home(), req.Options[idx].Image)
	if err != nil {
		http.Error(w, "invalid preview path", http.StatusBadRequest)
		return
	}
	f, err := os.Open(path)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || info.IsDir() {
		http.NotFound(w, r)
		return
	}
	http.ServeContent(w, r, info.Name(), info.ModTime(), f)
}
