include common.mk
include go.mk

# Dry run on purpose: the wet one writes a playlist to a real Apple Music library, which is not something
# `make run` should do by surprise.
.PHONY: run
run: ## Dry-run import of the 50-track sample list (writes nothing)
	@go run $(MAIN) import --dry-run --file summer-playlist-2026.txt --playlist "Summer 2026"
