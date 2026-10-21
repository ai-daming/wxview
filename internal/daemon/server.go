package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"wxview/internal/app"
	"wxview/internal/buildinfo"
	"wxview/internal/key"
	"wxview/internal/msgindex"
)

type Server struct {
	discovery  discoverySnapshot
	refreshMu  sync.Mutex
	SocketPath string
	Shutdown   func()

	mu     sync.Mutex
	target key.TargetDB

	indexMu sync.Mutex

	indexStateMu sync.Mutex
	indexRunning bool
	indexPending bool
}

func (s *Server) Run(ctx context.Context) error {
	if s.SocketPath == "" {
		return fmt.Errorf("socket path is empty")
	}
	if err := s.prepareSocket(ctx); err != nil {
		return err
	}
	listener, err := net.Listen("unix", s.SocketPath)
	if err != nil {
		return err
	}
	defer listener.Close()
	defer os.Remove(s.SocketPath)
	_ = os.Chmod(s.SocketPath, 0o600)
	if err := app.ChownForSudo(s.SocketPath); err != nil {
		return err
	}

	go s.watchContacts(ctx)
	go s.watchSessions(ctx)
	go s.watchMessages(ctx)
	go s.watchHeadImage(ctx)
	go s.watchFavorites(ctx)
	go s.watchSNS(ctx)
	if result, err := key.CleanTmp(); err != nil {
		log.Printf("cache tmp cleanup skipped: %v", err)
	} else if result.Removed > 0 {
		log.Printf("cache tmp cleanup removed %d files (%d bytes)", result.Removed, result.RemovedBytes)
	}
	s.refreshBackground(ctx, "startup")

	errCh := make(chan error, 1)
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				errCh <- err
				return
			}
			go s.handleConn(ctx, conn)
		}
	}()

	select {
	case <-ctx.Done():
		s.markIndexInterrupted()
		return nil
	case err := <-errCh:
		s.markIndexInterrupted()
		if errors.Is(err, net.ErrClosed) {
			return nil
		}
		return err
	}
}

func (s *Server) refreshBackground(ctx context.Context, reason string) {
	go func() {
		if err := s.refresh(ctx); err != nil {
			log.Printf("%s cache refresh failed: %v", reason, err)
			return
		}
		log.Printf("%s cache refresh completed", reason)
	}()
}

func (s *Server) prepareSocket(ctx context.Context) error {
	client := Client{SocketPath: s.SocketPath, Timeout: 500 * time.Millisecond}
	if client.Healthy(ctx) {
		return fmt.Errorf("daemon already running at %s", s.SocketPath)
	}
	if err := os.Remove(s.SocketPath); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

func (s *Server) refresh(ctx context.Context) error {
	if err := s.refreshContact(ctx); err != nil {
		return err
	}
	if err := s.refreshMessages(ctx); err != nil {
		return err
	}
	if err := s.refreshSessions(ctx); err != nil {
		log.Printf("refresh session cache skipped: %v", err)
	}
	s.refreshIndexBackground(ctx, "startup message cache refresh")
	if err := s.refreshHeadImage(ctx); err != nil {
		log.Printf("refresh head_image cache skipped: %v", err)
	}
	if err := s.refreshFavorites(ctx); err != nil {
		log.Printf("refresh favorite cache skipped: %v", err)
	}
	if err := s.refreshSNS(ctx); err != nil {
		log.Printf("refresh sns cache skipped: %v", err)
	}
	return nil
}

func (s *Server) refreshContact(ctx context.Context) error {
	s.refreshMu.Lock()
	defer s.refreshMu.Unlock()
	target, _, err := s.discovery.get()
	if err != nil {
		return err
	}
	res, _, err := key.EnsureDBCache(ctx, target)
	if err != nil {
		return err
	}
	s.mu.Lock()
	s.target = res.Target
	s.mu.Unlock()
	return nil
}

func (s *Server) refreshMessages(ctx context.Context) error {
	s.refreshMu.Lock()
	defer s.refreshMu.Unlock()
	_, targets, err := s.discovery.get()
	if err != nil {
		return err
	}
	for _, target := range targets {
		if _, _, err := key.EnsureDBCache(ctx, target); err != nil {
			if strings.HasPrefix(filepath.Base(target.DBPath), "message_") && strings.HasSuffix(filepath.Base(target.DBPath), ".db") {
				// Only numeric message shards are required; auxiliary caches are best effort.
				name := strings.TrimSuffix(strings.TrimPrefix(filepath.Base(target.DBPath), "message_"), ".db")
				if name != "" && strings.Trim(name, "0123456789") == "" {
					return err
				}
			}
			log.Printf("optional message cache refresh skipped: %v", err)
		}
	}
	return nil
}

func (s *Server) refreshIndex(ctx context.Context) error {
	s.refreshMu.Lock()
	defer s.refreshMu.Unlock()
	s.indexMu.Lock()
	defer s.indexMu.Unlock()
	target, _, err := s.discovery.get()
	if err != nil {
		return err
	}
	res, contactCache, err := key.EnsureDBCache(ctx, target)
	if err != nil {
		return err
	}
	messagePaths, err := key.AccountMessageCachePaths(ctx, target, true)
	if err != nil {
		return err
	}
	sessionPath := ""
	if _, path, err := key.EnsureDBCache(ctx, key.TargetFor(target, "session/session.db")); err != nil {
		log.Printf("refresh session cache skipped before index refresh: %v", err)
	} else {
		sessionPath = path
	}
	_, err = msgindex.Refresh(ctx, msgindex.BuildOptions{
		Account:           res.Target.Account,
		ContactCachePath:  contactCache,
		SessionCachePath:  sessionPath,
		MessageCachePaths: messagePaths,
	})
	return err
}

func (s *Server) markIndexInterrupted() {
	s.mu.Lock()
	account := s.target.Account
	s.mu.Unlock()
	if account == "" {
		target, err := key.DiscoverContactDB()
		if err != nil {
			return
		}
		account = target.Account
	}
	indexPath, err := msgindex.LocalPath(account)
	if err != nil {
		return
	}
	msgindex.MarkInterrupted(indexPath)
}

func (s *Server) refreshIndexBackground(ctx context.Context, reason string) {
	s.indexStateMu.Lock()
	if s.indexRunning {
		s.indexPending = true
		s.indexStateMu.Unlock()
		log.Printf("message index refresh already running; coalesced request after %s", reason)
		return
	}
	s.indexRunning = true
	s.indexStateMu.Unlock()

	go func() {
		currentReason := reason
		for {
			if err := s.refreshIndex(ctx); err != nil {
				log.Printf("message index refresh skipped after %s: %v", currentReason, err)
			} else {
				log.Printf("message index refreshed after %s", currentReason)
			}

			s.indexStateMu.Lock()
			if !s.indexPending {
				s.indexRunning = false
				s.indexStateMu.Unlock()
				return
			}
			s.indexPending = false
			s.indexStateMu.Unlock()
			currentReason = "coalesced request"
		}
	}()
}

func (s *Server) refreshSessions(ctx context.Context) error {
	s.refreshMu.Lock()
	defer s.refreshMu.Unlock()
	account, _, err := s.discovery.get()
	if err != nil {
		return err
	}
	target, err := existingTarget(account, "session/session.db")
	if err != nil {
		return err
	}
	_, _, err = key.EnsureDBCache(ctx, target)
	return err
}

func (s *Server) refreshHeadImage(ctx context.Context) error {
	s.refreshMu.Lock()
	defer s.refreshMu.Unlock()
	account, _, err := s.discovery.get()
	if err != nil {
		return err
	}
	target, err := existingTarget(account, "head_image/head_image.db")
	if err != nil {
		return err
	}
	_, _, err = key.EnsureDBCache(ctx, target)
	return err
}

func (s *Server) refreshFavorites(ctx context.Context) error {
	s.refreshMu.Lock()
	defer s.refreshMu.Unlock()
	account, _, err := s.discovery.get()
	if err != nil {
		return err
	}
	target, err := existingTarget(account, "favorite/favorite.db")
	if err != nil {
		return err
	}
	_, _, err = key.EnsureDBCache(ctx, target)
	return err
}

func (s *Server) refreshSNS(ctx context.Context) error {
	s.refreshMu.Lock()
	defer s.refreshMu.Unlock()
	account, _, err := s.discovery.get()
	if err != nil {
		return err
	}
	target, err := existingTarget(account, "sns/sns.db")
	if err != nil {
		return err
	}
	_, _, err = key.EnsureDBCache(ctx, target)
	return err
}

func (s *Server) watchContacts(ctx context.Context) {
	paths := func() []string { return s.watchPaths("contact/contact.db") }
	WatchFiles(ctx, paths, time.Second, time.Second, func() {
		if err := s.refreshContact(ctx); err != nil {
			log.Printf("refresh contact cache failed: %v", err)
			return
		}
		log.Printf("contact cache refreshed")
	})
}

func (s *Server) watchMessages(ctx context.Context) {
	paths := func() []string { return s.watchPaths("messages") }
	WatchFiles(ctx, paths, time.Second, time.Second, func() {
		if err := s.refreshMessages(ctx); err != nil {
			log.Printf("refresh message caches failed: %v", err)
			return
		}
		log.Printf("message caches refreshed")
		s.refreshIndexBackground(ctx, "message cache refresh")
	})
}

func (s *Server) watchSessions(ctx context.Context) {
	paths := func() []string { return s.watchPaths("session/session.db") }
	WatchFiles(ctx, paths, time.Second, time.Second, func() {
		if err := s.refreshSessions(ctx); err != nil {
			log.Printf("refresh session cache failed: %v", err)
			return
		}
		log.Printf("session cache refreshed")
	})
}

func (s *Server) watchHeadImage(ctx context.Context) {
	paths := func() []string { return s.watchPaths("head_image/head_image.db") }
	WatchFiles(ctx, paths, time.Second, time.Second, func() {
		if err := s.refreshHeadImage(ctx); err != nil {
			log.Printf("refresh head_image cache failed: %v", err)
			return
		}
		log.Printf("head_image cache refreshed")
	})
}

func (s *Server) watchFavorites(ctx context.Context) {
	paths := func() []string { return s.watchPaths("favorite/favorite.db") }
	WatchFiles(ctx, paths, time.Second, time.Second, func() {
		if err := s.refreshFavorites(ctx); err != nil {
			log.Printf("refresh favorite cache failed: %v", err)
			return
		}
		log.Printf("favorite cache refreshed")
	})
}

func (s *Server) watchSNS(ctx context.Context) {
	paths := func() []string { return s.watchPaths("sns/sns.db") }
	WatchFiles(ctx, paths, time.Second, time.Second, func() {
		if err := s.refreshSNS(ctx); err != nil {
			log.Printf("refresh sns cache failed: %v", err)
			return
		}
		log.Printf("sns cache refreshed")
	})
}

func (s *Server) handleConn(ctx context.Context, conn net.Conn) {
	defer conn.Close()
	var req Request
	if err := json.NewDecoder(conn).Decode(&req); err != nil {
		_ = json.NewEncoder(conn).Encode(Response{OK: false, Message: err.Error()})
		return
	}
	switch req.Action {
	case ActionHealth:
		stats := s.discovery.statistics()
		_ = json.NewEncoder(conn).Encode(Response{OK: true, Message: "ok", Version: buildinfo.String(), Discovery: &stats})
	case ActionRefreshContacts:
		if err := s.refreshContact(ctx); err != nil {
			_ = json.NewEncoder(conn).Encode(Response{OK: false, Message: err.Error()})
			return
		}
		_ = json.NewEncoder(conn).Encode(Response{OK: true, Message: "refreshed"})
	case ActionRefreshMessages:
		if err := s.refreshMessages(ctx); err != nil {
			_ = json.NewEncoder(conn).Encode(Response{OK: false, Message: err.Error()})
			return
		}
		_ = json.NewEncoder(conn).Encode(Response{OK: true, Message: "refreshed"})
		s.refreshIndexBackground(ctx, "refresh_messages action")
	case ActionRefreshIndex:
		if err := s.refreshIndex(ctx); err != nil {
			_ = json.NewEncoder(conn).Encode(Response{OK: false, Message: err.Error()})
			return
		}
		_ = json.NewEncoder(conn).Encode(Response{OK: true, Message: "refreshed"})
	case ActionRefreshSessions:
		if err := s.refreshSessions(ctx); err != nil {
			_ = json.NewEncoder(conn).Encode(Response{OK: false, Message: err.Error()})
			return
		}
		_ = json.NewEncoder(conn).Encode(Response{OK: true, Message: "refreshed"})
	case ActionRefreshAvatars:
		if err := s.refreshHeadImage(ctx); err != nil {
			_ = json.NewEncoder(conn).Encode(Response{OK: false, Message: err.Error()})
			return
		}
		_ = json.NewEncoder(conn).Encode(Response{OK: true, Message: "refreshed"})
	case ActionRefreshFavorites:
		if err := s.refreshFavorites(ctx); err != nil {
			_ = json.NewEncoder(conn).Encode(Response{OK: false, Message: err.Error()})
			return
		}
		_ = json.NewEncoder(conn).Encode(Response{OK: true, Message: "refreshed"})
	case ActionRefreshSNS:
		if err := s.refreshSNS(ctx); err != nil {
			_ = json.NewEncoder(conn).Encode(Response{OK: false, Message: err.Error()})
			return
		}
		_ = json.NewEncoder(conn).Encode(Response{OK: true, Message: "refreshed"})
	case ActionStop:
		s.markIndexInterrupted()
		_ = json.NewEncoder(conn).Encode(Response{OK: true, Message: "stopping"})
		if s.Shutdown != nil {
			go s.Shutdown()
		}
	default:
		_ = json.NewEncoder(conn).Encode(Response{OK: false, Message: "unknown daemon action: " + req.Action})
	}
}
