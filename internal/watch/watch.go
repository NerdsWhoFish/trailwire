package watch

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"time"
	"unicode"

	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/theoutdoorprogrammer/trailwire/internal/store"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/codes"
)

const defaultPollInterval = 250 * time.Millisecond

type Source interface {
	ObserveMessages(context.Context, int64, time.Time) ([]store.ObservedMessage, error)
}

type Options struct {
	MessageTTL   time.Duration
	PollInterval time.Duration
	Input        io.Reader
	Output       io.Writer
	Now          func() time.Time
}

type scope int

const (
	scopeAll scope = iota
	scopeRepo
	scopeChannel
	scopeDirect
)

type model struct {
	ctx          context.Context
	source       Source
	ttl          time.Duration
	pollInterval time.Duration
	now          func() time.Time
	viewport     viewport.Model
	messages     []store.ObservedMessage
	cursor       int64
	scope        scope
	width        int
	height       int
	ready        bool
	follow       bool
	loading      bool
	err          error
}

type refreshMsg struct {
	messages []store.ObservedMessage
	cutoff   time.Time
	err      error
}

type pollMsg time.Time

var (
	titleStyle    = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#7C5CFC"))
	liveStyle     = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#22C55E"))
	pausedStyle   = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#F59E0B"))
	errorStyle    = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#EF4444"))
	mutedStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("#6B7280"))
	activeTab     = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#F8FAFC")).Background(lipgloss.Color("#4F46E5")).Padding(0, 1)
	inactiveTab   = lipgloss.NewStyle().Foreground(lipgloss.Color("#94A3B8")).Padding(0, 1)
	timeStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("#64748B"))
	senderStyle   = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#E2E8F0"))
	repoStyle     = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#38BDF8"))
	channelStyle  = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#C084FC"))
	directStyle   = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#FB7185"))
	modifiedStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("#F59E0B"))
	recantedStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("#EF4444"))
	bodyStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("#CBD5E1"))
)

func Run(ctx context.Context, source Source, options Options) (err error) {
	ctx, span := otel.Tracer("github.com/theoutdoorprogrammer/trailwire/internal/watch").Start(ctx, "trailwire.watch")
	defer func() {
		if err != nil {
			span.RecordError(err)
			span.SetStatus(codes.Error, "watch failed")
		}
		span.End()
	}()
	if source == nil {
		return errors.New("message source is required")
	}
	if options.MessageTTL <= 0 {
		return errors.New("message TTL must be positive")
	}
	if options.PollInterval <= 0 {
		options.PollInterval = defaultPollInterval
	}
	if options.Input == nil {
		options.Input = os.Stdin
	}
	if options.Output == nil {
		options.Output = os.Stdout
	}
	if options.Now == nil {
		options.Now = time.Now
	}

	program := tea.NewProgram(
		newModel(ctx, source, options),
		tea.WithContext(ctx),
		tea.WithInput(options.Input),
		tea.WithOutput(options.Output),
	)
	_, err = program.Run()
	return err
}

func newModel(ctx context.Context, source Source, options Options) model {
	view := viewport.New(viewport.WithWidth(80), viewport.WithHeight(20))
	view.SoftWrap = true
	view.FillHeight = true
	return model{
		ctx:          ctx,
		source:       source,
		ttl:          options.MessageTTL,
		pollInterval: options.PollInterval,
		now:          options.Now,
		viewport:     view,
		follow:       true,
		loading:      true,
	}
}

func (m model) Init() tea.Cmd {
	return m.loadMessages()
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.ready = true
		m.resizeViewport()
		m.rebuildViewport()
		return m, nil
	case tea.KeyPressMsg:
		switch msg.String() {
		case "q", "ctrl+c":
			return m, tea.Quit
		case "tab":
			m.scope = (m.scope + 1) % 4
			m.follow = true
			m.rebuildViewport()
			return m, nil
		case "shift+tab":
			m.scope = (m.scope + 3) % 4
			m.follow = true
			m.rebuildViewport()
			return m, nil
		case "f":
			m.follow = true
			m.viewport.GotoBottom()
			return m, nil
		}
	case refreshMsg:
		m.loading = false
		m.err = msg.err
		if msg.err == nil {
			m.applyRefresh(msg)
		}
		return m, m.schedulePoll()
	case pollMsg:
		return m, m.loadMessages()
	}

	view, cmd := m.viewport.Update(msg)
	m.viewport = view
	if m.ready {
		m.follow = m.viewport.AtBottom()
	}
	return m, cmd
}

func (m model) View() tea.View {
	if !m.ready {
		view := tea.NewView("Loading Trailwire history…")
		view.AltScreen = true
		view.WindowTitle = "Trailwire watch"
		return view
	}

	header := m.header()
	tabs := m.tabs()
	footer := mutedStyle.Render("↑/↓ scroll  tab scope  f follow  q quit")
	content := strings.Join([]string{header, tabs, m.viewport.View(), footer}, "\n")
	view := tea.NewView(content)
	view.AltScreen = true
	view.WindowTitle = "Trailwire watch"
	return view
}

func (m model) loadMessages() tea.Cmd {
	after := m.cursor
	cutoff := m.now().Add(-m.ttl)
	return func() tea.Msg {
		messages, err := m.source.ObserveMessages(m.ctx, after, cutoff)
		return refreshMsg{messages: messages, cutoff: cutoff, err: err}
	}
}

func (m model) schedulePoll() tea.Cmd {
	return tea.Tick(m.pollInterval, func(now time.Time) tea.Msg { return pollMsg(now) })
}

func (m *model) applyRefresh(msg refreshMsg) {
	wasFollowing := m.follow || m.viewport.AtBottom()
	kept := m.messages[:0]
	for _, message := range m.messages {
		if !message.MessageCreatedAt.Before(msg.cutoff) {
			kept = append(kept, message)
		}
	}
	m.messages = append(kept, msg.messages...)
	for _, message := range msg.messages {
		if message.EventID > m.cursor {
			m.cursor = message.EventID
		}
	}
	m.follow = wasFollowing
	m.rebuildViewport()
}

func (m *model) resizeViewport() {
	height := m.height - 3
	if height < 1 {
		height = 1
	}
	m.viewport.SetWidth(max(m.width, 1))
	m.viewport.SetHeight(height)
}

func (m *model) rebuildViewport() {
	content := m.renderMessages()
	m.viewport.SetContent(content)
	if m.follow {
		m.viewport.GotoBottom()
	}
}

func (m model) header() string {
	status := liveStyle.Render("● LIVE")
	if !m.follow {
		status = pausedStyle.Render("● SCROLLED")
	}
	if m.loading {
		status = mutedStyle.Render("● SYNCING")
	}
	if m.err != nil {
		status = errorStyle.Render("● RETRYING")
	}

	repos := map[string]struct{}{}
	channels := map[string]struct{}{}
	agents := map[string]struct{}{}
	for _, message := range m.messages {
		agents[message.SenderID] = struct{}{}
		switch message.TargetKind {
		case "repo":
			repos[message.TargetID] = struct{}{}
		case "channel":
			channels[message.TargetID] = struct{}{}
		case "agent":
			agents[message.TargetID] = struct{}{}
		}
	}
	summary := fmt.Sprintf("%d events  %d repos  %d channels  %d agents", len(m.messages), len(repos), len(channels), len(agents))
	if m.err != nil {
		summary = safeText(m.err.Error())
	}
	return fmt.Sprintf("%s  %s  %s", titleStyle.Render("Trailwire"), status, mutedStyle.Render(summary))
}

func (m model) tabs() string {
	labels := []string{"All", "Repos", "Channels", "Direct"}
	tabs := make([]string, 0, len(labels))
	for index, label := range labels {
		style := inactiveTab
		if scope(index) == m.scope {
			style = activeTab
		}
		tabs = append(tabs, style.Render(label))
	}
	return strings.Join(tabs, " ")
}

func (m model) renderMessages() string {
	filtered := make([]store.ObservedMessage, 0, len(m.messages))
	for _, message := range m.messages {
		if m.matchesScope(message) {
			filtered = append(filtered, message)
		}
	}
	if len(filtered) == 0 {
		return mutedStyle.Render("No unexpired messages in this scope.")
	}

	sort.SliceStable(filtered, func(i, j int) bool { return filtered[i].EventID < filtered[j].EventID })
	blocks := make([]string, 0, len(filtered))
	for _, message := range filtered {
		blocks = append(blocks, renderMessage(message, max(m.width, 40)))
	}
	return strings.Join(blocks, "\n\n")
}

func (m model) matchesScope(message store.ObservedMessage) bool {
	switch m.scope {
	case scopeRepo:
		return message.TargetKind == "repo"
	case scopeChannel:
		return message.TargetKind == "channel"
	case scopeDirect:
		return message.TargetKind == "agent"
	default:
		return true
	}
}

func renderMessage(message store.ObservedMessage, width int) string {
	timestamp := timeStyle.Render(message.CreatedAt.Local().Format("15:04:05"))
	scopeLabel, target := targetLabels(message)
	meta := fmt.Sprintf("%s  %s  %s → %s", timestamp, scopeLabel, senderStyle.Render(safeText(message.SenderName)), safeText(target))
	if message.EventKind == "modified" {
		meta += "  " + modifiedStyle.Render(fmt.Sprintf("edited #%d", message.ID))
	}
	if message.EventKind == "recanted" {
		meta += "  " + recantedStyle.Render(fmt.Sprintf("recanted #%d", message.ID))
	}

	bodyWidth := max(width-2, 20)
	body := ansi.Wordwrap(safeText(message.Body), bodyWidth, "")
	if message.EventKind == "recanted" && strings.TrimSpace(body) == "" {
		body = "Message withdrawn"
	}
	return meta + "\n" + bodyStyle.PaddingLeft(2).Render(body)
}

func targetLabels(message store.ObservedMessage) (string, string) {
	switch message.TargetKind {
	case "repo":
		return repoStyle.Render("repo"), strings.TrimPrefix(message.TargetID, "github.com/")
	case "channel":
		return channelStyle.Render("channel"), "#" + strings.TrimPrefix(message.TargetID, "#")
	default:
		target := message.TargetName
		if target == "" {
			target = message.TargetID
		}
		return directStyle.Render("direct"), target
	}
}

func safeText(value string) string {
	value = ansi.Strip(value)
	return strings.Map(func(character rune) rune {
		switch character {
		case '\n':
			return character
		case '\t':
			return ' '
		}
		if unicode.IsControl(character) || unicode.Is(unicode.Cf, character) {
			return -1
		}
		return character
	}, value)
}
