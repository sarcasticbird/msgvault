package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
	"github.com/wesm/msgvault/internal/store"
)

var emptyTrashCmd = &cobra.Command{
	Use:   "empty-trash",
	Short: "Permanently remove trashed messages from local database",
	Long: `Delete all messages with the TRASH label from the local archive.

This only affects the local database — it does NOT delete messages from Gmail.
Messages in trash are typically already deleted upstream; this command cleans
up the local copy to reclaim space and keep the archive tidy.

Use --account to limit to a specific account.`,
	RunE: runEmptyTrash,
}

var emptyTrashAccount string

func init() {
	emptyTrashCmd.Flags().StringVar(&emptyTrashAccount, "account", "", "Only purge trash for this account")
	rootCmd.AddCommand(emptyTrashCmd)
}

func runEmptyTrash(cmd *cobra.Command, args []string) error {
	s, err := store.Open(cfg.DatabaseDSN())
	if err != nil {
		return fmt.Errorf("open database: %w", err)
	}
	defer s.Close()

	if err := s.InitSchema(); err != nil {
		return fmt.Errorf("init schema: %w", err)
	}

	var count int64

	if emptyTrashAccount != "" {
		source, err := s.GetSourceByIdentifier(emptyTrashAccount)
		if err != nil {
			return fmt.Errorf("get source: %w", err)
		}
		if source == nil {
			return fmt.Errorf("account not found: %s", emptyTrashAccount)
		}
		count, err = s.PurgeTrashMessagesForSource(source.ID)
		if err != nil {
			return fmt.Errorf("purge trash: %w", err)
		}
	} else {
		count, err = s.PurgeTrashMessages()
		if err != nil {
			return fmt.Errorf("purge trash: %w", err)
		}
	}

	if count == 0 {
		fmt.Println("No trashed messages found.")
	} else {
		fmt.Printf("Purged %d trashed messages from local database.\n", count)
	}

	return nil
}
