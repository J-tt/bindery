-- Bug 8: add per-author audiobook root folder so audiobooks can be routed to a
-- different directory than the global BINDERY_AUDIOBOOK_DIR.
ALTER TABLE authors ADD COLUMN audiobook_root_folder_id INTEGER REFERENCES root_folders(id) ON DELETE SET NULL;
