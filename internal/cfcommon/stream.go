package cfcommon

import (
	"bufio"
	"encoding/csv"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

type CSVRecordSink struct {
	mu       sync.Mutex
	path     string
	writer   *csv.Writer
	closeFn  func() error
	closed   bool
	rows     int
	firstErr error // 记录第一次写入失败，让 Close 能上报被 _ = 吞掉的错误
}

func NewCSVRecordSink(path string, header []string) (*CSVRecordSink, error) {
	path = filepath.Clean(path)
	w, _, closeFn, err := NewCSVWriter(path)
	if err != nil {
		return nil, err
	}
	sink := &CSVRecordSink{path: path, writer: w, closeFn: closeFn}
	if len(header) > 0 {
		if err := sink.writeLocked(header, false); err != nil {
			_ = sink.closeFn()
			return nil, err
		}
	}
	return sink, nil
}

func NewCSVAppendSink(path string, header []string) (*CSVRecordSink, error) {
	path = filepath.Clean(path)
	w, _, closeFn, _, err := NewCSVAppendWriter(path, header)
	if err != nil {
		return nil, err
	}
	return &CSVRecordSink{path: path, writer: w, closeFn: closeFn}, nil
}

func (s *CSVRecordSink) Write(record []string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.writeLocked(record, true)
}

func (s *CSVRecordSink) Rows() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.rows
}

func (s *CSVRecordSink) Close() error {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	s.closed = true
	return errors.Join(s.closeFn(), s.firstErr)
}

// writeLocked 记录第一次失败，这样即使调用点用 `_ = sink.Write(...)` 吞掉了
// 返回值，错误仍会在 Close() 里被统一上报。
func (s *CSVRecordSink) writeLocked(record []string, countRow bool) error {
	err := s.writeUnlocked(record, countRow)
	if err != nil && s.firstErr == nil {
		s.firstErr = err
	}
	return err
}

func (s *CSVRecordSink) writeUnlocked(record []string, countRow bool) error {
	if s.closed {
		return wrapPathError("write csv", s.path, fmt.Errorf("sink already closed"))
	}
	if err := s.writer.Write(record); err != nil {
		return wrapPathError("write csv", s.path, err)
	}
	s.writer.Flush()
	if err := s.writer.Error(); err != nil {
		return wrapPathError("flush csv", s.path, err)
	}
	if countRow {
		s.rows++
	}
	return nil
}

type LineSink struct {
	mu       sync.Mutex
	path     string
	file     *os.File
	writer   *bufio.Writer
	closed   bool
	lines    int
	firstErr error
}

func NewLineSink(path string) (*LineSink, error) {
	path = filepath.Clean(path)
	if err := ensureParentDir(path); err != nil {
		return nil, wrapPathError("prepare output path", path, err)
	}
	f, err := os.Create(path)
	if err != nil {
		return nil, wrapPathError("open file", path, err)
	}
	return &LineSink{
		path:   path,
		file:   f,
		writer: bufio.NewWriter(f),
	}, nil
}

func NewLineAppendSink(path string) (*LineSink, error) {
	path = filepath.Clean(path)
	if err := ensureParentDir(path); err != nil {
		return nil, wrapPathError("prepare output path", path, err)
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return nil, wrapPathError("open file", path, err)
	}
	return &LineSink{
		path:   path,
		file:   f,
		writer: bufio.NewWriter(f),
	}, nil
}

func (s *LineSink) WriteLine(line string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	err := s.writeLineLocked(line)
	if err != nil && s.firstErr == nil {
		s.firstErr = err
	}
	return err
}

func (s *LineSink) writeLineLocked(line string) error {
	if s.closed {
		return wrapPathError("write file", s.path, fmt.Errorf("sink already closed"))
	}
	if _, err := s.writer.WriteString(line + "\n"); err != nil {
		return wrapPathError("write file", s.path, err)
	}
	if err := s.writer.Flush(); err != nil {
		return wrapPathError("flush file", s.path, err)
	}
	s.lines++
	return nil
}

func (s *LineSink) Lines() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.lines
}

func (s *LineSink) Close() error {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	s.closed = true
	if err := s.writer.Flush(); err != nil {
		_ = s.file.Close()
		return errors.Join(wrapPathError("flush file", s.path, err), s.firstErr)
	}
	if err := s.file.Close(); err != nil {
		return errors.Join(wrapPathError("close file", s.path, err), s.firstErr)
	}
	return s.firstErr
}
