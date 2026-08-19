package musicapp

// The AppleScript below is passed to `osascript -` on stdin, with playlist and
// track names supplied as run-handler arguments. Two rules keep it working:
//
//   - never interpolate a name into the script text: names contain quotes,
//     backslashes and curly apostrophes, and argv sidesteps all of it;
//   - never write "--" inside an AppleScript block comment: it opens a nested
//     line comment that swallows the closing delimiter. There are no block
//     comments here for that reason.

// joinLines is appended to every script that returns tabular output.
const joinLines = `

on joinLines(theItems)
	set saved to AppleScript's text item delimiters
	set AppleScript's text item delimiters to linefeed
	set joined to theItems as text
	set AppleScript's text item delimiters to saved
	return joined
end joinLines
`

const scriptPlaylists = `on run argv
	set out to {}
	tell application "Music"
		repeat with p in (every user playlist whose special kind is none and smart is false)
			set end of out to (name of p) & tab & (persistent ID of p) & tab & ((count of tracks of p) as text)
		end repeat
	end tell
	return joinLines(out)
end run
` + joinLines

const scriptTracks = `on run argv
	set plName to item 1 of argv
	set out to {}
	tell application "Music"
		set pls to (every user playlist whose name is plName and special kind is none)
		if (count of pls) is 0 then error "musickit:no-playlist"
		set pl to item 1 of pls
		set n to count of tracks of pl
		repeat with i from 1 to n
			set t to track i of pl
			set end of out to (i as text) & tab & (persistent ID of t) & tab & (artist of t) & tab & (name of t) & tab & (album of t)
		end repeat
	end tell
	return joinLines(out)
end run
` + joinLines

// scriptRemove takes the playlist name, then one "index:persistentID" argument
// per track, highest index first. Each position is checked against its ID
// before the delete, so a playlist that changed under us is skipped rather
// than mangled.
const scriptRemove = `on run argv
	set plName to item 1 of argv
	set out to {}
	tell application "Music"
		set pls to (every user playlist whose name is plName and special kind is none)
		if (count of pls) is 0 then error "musickit:no-playlist"
		set pl to item 1 of pls
		repeat with i from 2 to (count of argv)
			set saved to AppleScript's text item delimiters
			set AppleScript's text item delimiters to ":"
			set parts to text items of (item i of argv)
			set AppleScript's text item delimiters to saved
			set idx to (item 1 of parts) as integer
			set pid to (item 2 of parts) as text
			set outcome to "SKIP"
			try
				set t to track idx of pl
				if (persistent ID of t) is pid then
					delete t
					set outcome to "OK"
				end if
			on error
				set outcome to "SKIP"
			end try
			set end of out to outcome & tab & pid
		end repeat
	end tell
	return joinLines(out)
end run
` + joinLines

const scriptRename = `on run argv
	set plName to item 1 of argv
	set newName to item 2 of argv
	tell application "Music"
		set pls to (every user playlist whose name is plName and special kind is none)
		if (count of pls) is 0 then error "musickit:no-playlist"
		set name of (item 1 of pls) to newName
	end tell
	return ""
end run
`

const scriptDelete = `on run argv
	set plName to item 1 of argv
	tell application "Music"
		set pls to (every user playlist whose name is plName and special kind is none)
		if (count of pls) is 0 then error "musickit:no-playlist"
		delete (item 1 of pls)
	end tell
	return ""
end run
`

const scriptCreate = `on run argv
	set plName to item 1 of argv
	tell application "Music"
		make new user playlist with properties {name:plName}
	end tell
	return ""
end run
`
