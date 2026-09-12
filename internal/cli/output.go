package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"
)

// Print writes v to w as indented JSON when jsonMode is true, or as a
// plain-text table otherwise: a slice of structs (or a single struct,
// treated as a one-row slice) renders as a tabwriter table with a header
// row of exported field names; a map[string]any renders as sorted
// key/value rows; anything else (a scalar, or a slice of scalars) is
// printed plainly.
func Print(w io.Writer, jsonMode bool, v any) error {
	if jsonMode {
		raw, err := json.MarshalIndent(v, "", "  ")
		if err != nil {
			return fmt.Errorf("cli: marshal json: %w", err)
		}
		_, err = fmt.Fprintln(w, string(raw))
		return err
	}
	return printTable(w, v)
}

func printTable(w io.Writer, v any) error {
	if v == nil {
		return nil
	}
	rv := reflect.ValueOf(v)
	switch rv.Kind() {
	case reflect.Map:
		return printMapRows(w, rv)
	case reflect.Struct:
		one := reflect.MakeSlice(reflect.SliceOf(rv.Type()), 1, 1)
		one.Index(0).Set(rv)
		return printStructRows(w, one)
	case reflect.Slice, reflect.Array:
		if rv.Type().Elem().Kind() == reflect.Struct {
			return printStructRows(w, rv)
		}
	}
	_, err := fmt.Fprintln(w, v)
	return err
}

// printStructRows renders a slice (or array) of structs as a tabwriter
// table: a header row of exported field names, then one row per element.
func printStructRows(w io.Writer, rv reflect.Value) error {
	t := rv.Type().Elem()
	var fields []int
	var header []string
	for i := 0; i < t.NumField(); i++ {
		if t.Field(i).IsExported() {
			fields = append(fields, i)
			header = append(header, t.Field(i).Name)
		}
	}
	tw := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
	if _, err := fmt.Fprintln(tw, strings.Join(header, "\t")); err != nil {
		return err
	}
	for i := 0; i < rv.Len(); i++ {
		item := rv.Index(i)
		cells := make([]string, len(fields))
		for j, fi := range fields {
			cells[j] = fmt.Sprint(item.Field(fi).Interface())
		}
		if _, err := fmt.Fprintln(tw, strings.Join(cells, "\t")); err != nil {
			return err
		}
	}
	return tw.Flush()
}

// printMapRows renders a map as sorted "key\tvalue" rows, no header.
func printMapRows(w io.Writer, rv reflect.Value) error {
	type row struct {
		key string
		val any
	}
	rows := make([]row, 0, rv.Len())
	for _, k := range rv.MapKeys() {
		rows = append(rows, row{fmt.Sprint(k.Interface()), rv.MapIndex(k).Interface()})
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].key < rows[j].key })
	tw := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
	for _, r := range rows {
		if _, err := fmt.Fprintf(tw, "%s\t%v\n", r.key, r.val); err != nil {
			return err
		}
	}
	return tw.Flush()
}

// simpleCmd builds a no-argument, read-only Cobra command: it builds the
// client via get, invokes call, and prints the result honouring --json.
func simpleCmd[C any](use, short string, get func() (C, error), flags *GlobalFlags, call func(context.Context, C) (any, error)) *cobra.Command {
	return &cobra.Command{
		Use:   use,
		Short: short,
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, err := get()
			if err != nil {
				return err
			}
			v, err := call(cmd.Context(), c)
			if err != nil {
				return err
			}
			return Print(cmd.OutOrStdout(), flags.JSON, v)
		},
	}
}

// doOrDryRun implements the shared write-verb contract: with --dry-run it
// prints "would <desc>" and returns without calling do; otherwise it calls
// do and prints its result honouring --json.
func doOrDryRun(cmd *cobra.Command, flags *GlobalFlags, desc string, do func() (any, error)) error {
	if flags.DryRun {
		_, err := fmt.Fprintf(cmd.OutOrStdout(), "would %s\n", desc)
		return err
	}
	v, err := do()
	if err != nil {
		return err
	}
	return Print(cmd.OutOrStdout(), flags.JSON, v)
}

// parseSince parses a Go duration ("24h", "30m") or a day count ("30d") as
// a point in time that far in the past. The empty string means "no lower
// bound" and returns the zero time.
func parseSince(s string) (time.Time, error) {
	if s == "" {
		return time.Time{}, nil
	}
	if days, ok := strings.CutSuffix(s, "d"); ok {
		n, err := strconv.Atoi(days)
		if err != nil {
			return time.Time{}, fmt.Errorf("parse --since %q: %w", s, err)
		}
		return time.Now().Add(-time.Duration(n) * 24 * time.Hour), nil
	}
	d, err := time.ParseDuration(s)
	if err != nil {
		return time.Time{}, fmt.Errorf("parse --since %q: %w", s, err)
	}
	return time.Now().Add(-d), nil
}

// parseID parses a positional CLI argument as an int64 resource id.
func parseID(s string) (int64, error) {
	id, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("parse id %q: %w", s, err)
	}
	return id, nil
}

// parseOnOff parses a positional "on"/"off" argument as a bool.
func parseOnOff(s string) (bool, error) {
	switch s {
	case "on":
		return true, nil
	case "off":
		return false, nil
	default:
		return false, fmt.Errorf("invalid state %q: want \"on\" or \"off\"", s)
	}
}
