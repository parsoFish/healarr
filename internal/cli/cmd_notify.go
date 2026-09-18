package cli

import (
	"errors"
	"fmt"
	"time"

	"github.com/spf13/cobra"

	"github.com/parsoFish/healarr/internal/version"
)

// errNoSender is returned by `notify test` when this node has no email
// sender configured — only the Pi ever sends mail (constraints.md: "the
// NAS never calls the sender").
var errNoSender = errors.New("email is only configured on the pi node")

// errNoRecipient is returned by `notify test` when neither --to nor
// [email] to names an address. The Pi has a sender whether or not email.to
// is set (see defaultSender), so a missing recipient is a configuration
// error to report, not a reason to quietly do nothing.
var errNoRecipient = errors.New("no recipient: set [email] to or pass --to")

func newNotifyCmd(deps *Deps, flags *GlobalFlags) *cobra.Command {
	root := &cobra.Command{Use: "notify", Short: "Send notifications"}
	root.AddCommand(notifyTestCmd(deps, flags))
	return root
}

func notifyTestCmd(deps *Deps, flags *GlobalFlags) *cobra.Command {
	var to string
	cmd := &cobra.Command{
		Use:   "test",
		Short: "Send one short test email (--dry-run: send without recording it in the outbox)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runNotifyTest(cmd, deps, flags, to)
		},
	}
	cmd.Flags().StringVar(&to, "to", "", "recipient address (default: cfg.Email.To)")
	return cmd
}

// runNotifyTest sends one short test email through this node's sender. A
// nil sender (errNoSender) means this node was never meant to send mail at
// all, and no resolvable recipient (errNoRecipient: neither --to nor
// email.to) means it has nowhere to send this one; either way nothing is
// enqueued or attempted. Otherwise, unless --dry-run, the message is
// enqueued in the outbox before it is sent and marked sent/failed
// afterward, so the outbox reflects every attempt made (mirroring
// agent.SendDigest); --dry-run sends without touching the store.
func runNotifyTest(cmd *cobra.Command, deps *Deps, flags *GlobalFlags, to string) (err error) {
	ctx := cmd.Context()
	cfg, _, err := deps.load(flags)
	if err != nil {
		return err
	}

	sender := senderFor(deps, cfg)
	if sender == nil {
		return errNoSender
	}

	addr := to
	if addr == "" {
		addr = cfg.Email.To
	}
	if addr == "" {
		return errNoRecipient
	}
	now := time.Now()
	subject := fmt.Sprintf("healarr test email from %s", cfg.Node)
	body := fmt.Sprintf("healarr %s\nsent at %s\n", version.Version, now.Format(time.RFC3339))

	if flags.DryRun {
		if sendErr := sender.Send(ctx, addr, subject, body); sendErr != nil {
			return fmt.Errorf("notify test: send: %w", sendErr)
		}
		_, err = fmt.Fprintln(cmd.OutOrStdout(), "sent (not recorded)")
		return err
	}

	st, err := openAgentStore(ctx, deps, cfg)
	if err != nil {
		return fmt.Errorf("notify test: open store: %w", err)
	}
	defer func() {
		if closeErr := st.Close(); closeErr != nil {
			err = errors.Join(err, fmt.Errorf("notify test: close store: %w", closeErr))
		}
	}()

	id, err := st.EnqueueEmail(ctx, addr, subject, body, now)
	if err != nil {
		return fmt.Errorf("notify test: enqueue: %w", err)
	}

	if sendErr := sender.Send(ctx, addr, subject, body); sendErr != nil {
		wrapped := fmt.Errorf("notify test: send: %w", sendErr)
		if markErr := st.MarkEmailFailed(ctx, id, time.Now(), sendErr); markErr != nil {
			return errors.Join(wrapped, fmt.Errorf("notify test: mark failed: %w", markErr))
		}
		return wrapped
	}

	if markErr := st.MarkEmailSent(ctx, id, time.Now()); markErr != nil {
		return fmt.Errorf("notify test: mark sent: %w", markErr)
	}

	_, err = fmt.Fprintf(cmd.OutOrStdout(), "outbox id: %d\n", id)
	return err
}
