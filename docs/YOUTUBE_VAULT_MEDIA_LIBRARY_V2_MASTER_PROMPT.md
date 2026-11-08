# MASTER DEVELOPMENT PROMPT
## YouTube Vault — Media Library 2.0
### Complete UI/UX Redesign and Implementation

**Repository:** https://github.com/ajbergh/yt-dl-go  
**Project:** YouTube Vault  
**Objective:** Transform the existing Download Library into a premium, modern, media-centric experience inspired by Plex, Jellyfin, and other leading media management applications.

**Visual reference:** The attached Media Library 2.0 mockup is the primary design target. The original application screenshots illustrate the current implementation and should be used for comparison.

---

# 1. Mission

Redesign and implement the YouTube Vault Media Library to deliver a significantly improved experience for browsing, searching, previewing, organizing, and managing downloaded video and audio content.

The current library is functional but presents downloads primarily as job-oriented records with nested file information. This makes it feel more like a download manager than a modern media library.

The new experience should be:

- **Media-first:** Thumbnails, titles, channels, and content should dominate the interface.
- **Visually premium:** Modern dark-mode styling, subtle animations, consistent spacing, sophisticated typography, and restrained accent colors.
- **Highly functional:** Every visible interaction must be connected to real application behavior.
- **Fast and responsive:** Smooth performance with large libraries containing thousands of files.
- **Cross-platform:** Compatible with every currently supported desktop platform.
- **Architecturally sound:** Reuse and extend existing application systems rather than introducing unnecessary frameworks or duplicating functionality.

The goal is not merely to reproduce the screenshot. The goal is to implement a production-quality media management experience that matches the design direction while improving workflows and usability.

**Execute the implementation, not just a design review or planning exercise.**

---

# 2. Initial Repository Assessment

Before modifying code, perform a thorough review of the existing repository.

Identify:

1. Application framework, frontend stack, styling system, and component architecture.
2. Existing navigation and routing implementation.
3. Current media library UI components.
4. Download-job data model and persisted history.
5. How downloaded files are discovered, indexed, and associated with download jobs.
6. Existing thumbnail and metadata retrieval.
7. Video/audio preview and playback capabilities.
8. Search, filtering, and sorting implementations.
9. File management operations, including Reveal, Copy Path, and Delete.
10. Existing tests, linting rules, build processes, and GitHub Actions workflows.

Document the findings briefly before implementation.

Use the current framework and existing conventions. Do not rewrite the entire application or replace the frontend stack unless a demonstrable technical limitation makes that necessary.

Preserve the current downloader engine, queue processing, configuration, and existing user data.

# 3. Visual Design System

Implement a cohesive design system based on the attached mockup.

### Colors

Use a premium dark theme with the following approximate palette:

| Element | Reference Color |
|---|---|
| Application background | `#0B0C0F` |
| Sidebar background | `#101114` |
| Elevated surfaces | `#17181D` |
| Media cards | `#191A20` |
| Interactive hover surfaces | `#25262D` |
| Primary accent | `#F20546` |
| Accent hover | `#FF3366` |
| Primary text | `#F5F5F7` |
| Secondary text | `#A2A4AF` |
| Dividers and borders | `#2B2D35` |
| Success/status | `#00C896` |
| Warning | `#F5B544` |
| Error/destructive | `#F05B68` |

Treat these colors as starting values. Adjust them to achieve strong contrast and visual consistency.

Centralize all design tokens using the repository's existing styling approach.

### Typography

Use a modern, readable sans-serif font consistent with the current application's typography and packaging requirements.

Establish clear hierarchy for:

- Page titles
- Section headings
- Media titles
- Creator/channel information
- Technical metadata
- Status indicators
- Secondary actions

Avoid excessively small text and unnecessary uppercase labels.

### UI Styling

Implement:

- Consistent border radii.
- Subtle one-pixel borders.
- Restrained shadows and accent glows.
- Smooth hover and selection transitions.
- Clearly differentiated active, hovered, and disabled states.
- Consistent icon sizing and alignment.
- Adequate spacing and whitespace.
- Accessible keyboard-focus indicators.

Avoid excessive gradients, decorative illustrations, or animations that distract from the media.

The interface should feel like a professional desktop media application, not a generic administrative dashboard.

---

# 4. Application Layout

Follow the primary composition of the mockup.

The redesigned Library should consist of:

**A. Global application header**  
**B. Left navigation sidebar**  
**C. Main media library workspace**  
**D. Contextual media details panel**

Use an adaptive layout that scales gracefully with available window space.

## 4.1 Global Header

Preserve the YouTube Vault brand identity.

Provide access to the application's existing major workflows, such as Queue, Library, and Settings.

The header should support:

- Application identity.
- Current section.
- Queue count when relevant.
- Application/download engine status.
- Prominent Add Links action.
- Existing platform-specific window behavior.

Avoid duplicating navigation unnecessarily between the header and sidebar.

If the existing app has a different navigation model, reconcile it with the mockup rather than creating competing systems.

## 4.2 Left Navigation Sidebar

Implement a persistent, collapsible sidebar with appropriate icons.

Target navigation:

- Home or Library Overview
- All Media
- Videos
- Audio
- Channels
- Playlists
- Collections
- Favorites
- Recently Added

Show relevant counts where available.

Active navigation items should have a clear accent-colored selection state.

Add a compact storage summary near the bottom showing actual library usage.

Do not invent a storage quota or capacity. If filesystem capacity is available, display it accurately and label it appropriately.

Support:

- Expanded sidebar
- Collapsed icon-only sidebar
- Tooltips in collapsed mode
- Keyboard navigation
- Responsive behavior for smaller application windows

Only expose fully functional destinations. New functionality such as Collections or Favorites must be implemented with appropriate persistence if included.

---

# 5. Library Overview

Replace the existing job-centric summary with a more polished media-oriented overview.

Include a compact information area showing:

- Total indexed media files
- Total local media storage used
- Recently added media
- Optional quick actions

All displayed values must derive from actual library data.

The overview should be visually restrained, leaving maximum space for media browsing.

For small libraries, show relevant onboarding or discovery actions rather than excessive empty dashboard components.

For larger libraries, emphasize browsing and filtering.

---

# 6. Media Library Grid

This is the most important element of the redesign.

## 6.1 Media-First Cards

Replace the existing large, nested job cards with individual media cards.

Each card should display:

- Large 16:9 media thumbnail for videos
- Appropriate album artwork or fallback presentation for audio
- Media title, truncated intelligently
- Channel/creator name when available
- Duration badge
- Resolution or quality indicator
- File size
- File format when useful
- Favorite control
- Contextual actions

The thumbnail should be the dominant visual element.

Download completion status should not overwhelm the interface. Completed items should generally look like normal media, with detailed status shown only when useful.

## 6.2 Card Interactions

Support:

- Single click to select
- Double click or explicit Play action to preview/open
- Hover to reveal contextually relevant actions
- Right-click context menu, where supported
- Keyboard selection
- Multi-selection using standard desktop interaction conventions
- Favorite/unfavorite
- Reveal in file manager
- Copy file path
- Delete with confirmation

Selected cards should have a clear visual highlight.

Avoid creating large button rows that consume valuable thumbnail space.

## 6.3 Adaptive Grid

Use a responsive CSS grid or the equivalent supported by the existing stack.

Cards should resize intelligently while maintaining thumbnail aspect ratio.

Support common desktop widths, including:

- 1280px
- 1366px
- 1600px
- 1920px
- Ultrawide displays

The number of columns should change based on available workspace width, including whether the details panel is open.

## 6.4 List View

Implement a genuine alternative List View.

List View should provide a compact, sortable table with columns such as:

- Thumbnail
- Title
- Channel
- Media type
- Duration
- Resolution/quality
- File size
- Date added
- Status

Support sorting and multi-selection.

The Grid/List preference should persist across application restarts.

Both views must operate on the same underlying library data and filtering state.

---

# 7. Search, Filtering, and Sorting

Implement a high-quality, immediate search experience.

Search should support available fields such as:

- Video/audio title
- Channel/creator
- File name
- Tags or categories
- Playlist
- Collection

### Filter Controls

Provide useful filter options, including:

- All Media
- Videos
- Audio
- Favorites
- Recently Added
- Completed
- In Progress
- Failed

Additional advanced filters may include:

- Resolution
- File format
- Channel
- Download date
- File size range
- Duration range

Only apply filters to fields actually supported by the data model.

### Sorting

Support:

- Recently added
- Oldest first
- Title A–Z
- Title Z–A
- Largest file
- Smallest file
- Longest duration
- Shortest duration
- Channel

Persist user sorting preferences when appropriate.

Search and filter operations must be efficient for large libraries.

Debounce search input where necessary and avoid unnecessary full-list rendering.

Include clear empty search result states and an obvious way to reset active filters.

---

# 8. Media Details Inspector

Implement the contextual right-side media details panel shown in the mockup.

The panel should open when a media item is selected.

Display:

- Large media thumbnail or artwork
- Media title
- Creator/channel
- Media type
- Duration
- Resolution
- File size
- Format
- Download date
- Local filename
- Local directory
- Available metadata
- Playlist and collection associations
- User-defined tags, if implemented

Show the original description only when it is available from the media metadata.

Do not generate fictional descriptions, subscriber counts, tags, or file information.

### Primary Actions

- Play / Preview
- Reveal in file manager
- Copy path
- Open containing folder
- Favorite
- Add to collection
- Delete

Use consistent action placement and clear destructive-action treatment.

The inspector should:

- Be collapsible.
- Update without navigating away from the library.
- Preserve the active selection.
- Support narrower desktop windows.
- Handle long filenames and metadata cleanly.
- Avoid unnecessary rerendering.

The user should be able to browse multiple media items while the inspector updates dynamically.

---

# 9. Audio Library Experience

The application downloads both video and audio. The redesign must treat audio as a first-class media type.

Do not render audio files as video cards with broken thumbnails.

Implement audio-specific presentation using:

- Album artwork when available
- Attractive fallback artwork
- Track title
- Artist/channel
- Duration
- Audio format
- Bitrate when available
- File size

Provide a useful audio preview experience.

Prefer reusing the existing media playback system. If none exists, evaluate a lightweight implementation compatible with supported desktop platforms.

Avoid unnecessary heavyweight media dependencies.

Audio and video should share common library infrastructure but use specialized presentation components when appropriate.

---

# 10. Channels, Playlists, and Collections

Improve content organization beyond individual files.

### Channels

When creator metadata is available, group media by channel.

Allow users to open a channel view containing all associated downloaded content.

### Playlists

Preserve existing playlist associations from download metadata.

Allow users to browse media by source playlist where supported.

### Collections

Implement optional user-defined collections that let users organize downloaded content independently of the source playlist.

Support:

- Create collection
- Rename collection
- Add/remove media
- Delete collection without deleting underlying files
- Collection thumbnails derived from contained media

Persist collections locally using the application's existing storage architecture.

Ensure collection membership survives restarts and library refreshes.

Do not conflate source playlists with user-created collections.

---

# 11. Library Data Architecture

One of the most important goals is to decouple media browsing from the current download-job presentation.

Inspect the existing data structures before making changes.

Establish a reliable model for individual downloaded media files.

Where needed, create a normalized library item representation with fields resembling:

- Stable media item ID
- Source URL or source identifier
- Download job ID
- Local file path
- Filename
- Media type
- Title
- Creator/channel
- Thumbnail reference
- Duration
- Resolution
- File size
- Format
- Date added
- Download status
- Playlist associations
- Favorite state
- Collection memberships
- User metadata

Adapt this model to the real application.

Do not introduce unnecessary database migrations or duplicate persisted records if existing models can be extended safely.

### Critical Requirements

1. Preserve existing downloaded media and history.
2. Correctly represent multiple media files associated with a single download job.
3. Handle missing or moved files.
4. Support library rescanning and metadata refresh where appropriate.
5. Avoid duplicate library entries.
6. Handle missing metadata gracefully.
7. Maintain cross-platform file-path correctness.
8. Never assume Windows-only directory conventions.
9. Do not issue expensive filesystem scans on every UI render.
10. Ensure the library can scale to thousands of entries.

Define a stable separation between downloaded jobs, media file records, and user-organizational metadata.

---

# 12. Playback and Preview

Preserve current preview capabilities and improve their discoverability.

Clicking Play should launch the existing supported playback mechanism.

If media playback is embedded, include:

- Play/pause
- Seek
- Volume
- Fullscreen for video
- Playback position
- Useful codec error handling

If the existing architecture uses an external player, retain that behavior unless an embedded solution can be introduced reliably.

Do not assume all downloaded formats are supported by the embedded browser engine.

Provide a clear fallback for unsupported codecs and containers.

Do not allow preview changes to disrupt downloading or queue operations.

---

# 13. File Management and Safety

Retain all existing file management functionality.

Implement safe behaviors for:

- Reveal file
- Open containing folder
- Copy path
- Delete file
- Remove from library
- Delete multiple files

Distinguish clearly between removing a library record and permanently deleting the underlying file.

For destructive operations:

- Display an explicit confirmation.
- Identify the affected files.
- Explain whether the underlying filesystem content will be deleted.
- Handle partial failures safely.
- Update the library state after successful deletion.
- Preserve the ability to cancel.

Never silently delete files as a side effect of reorganizing collections or playlists.

---

# 14. Interaction and UX Polish

Implement the following behaviors where appropriate:

- Skeleton loading states
- Thumbnail lazy loading
- Smooth selection transitions
- Nonintrusive notifications
- Keyboard shortcuts
- Context menus
- Tooltips
- Empty library state
- No search results state
- Missing file state
- Failed thumbnail fallback
- Undo for reversible organizational changes
- Persistent view preferences

Recommended shortcuts:

- `Ctrl/Cmd + F`: Focus library search
- `Escape`: Clear focus or close contextual overlays
- `Enter`: Play or open selected media
- `Delete`: Initiate confirmed delete
- `Ctrl/Cmd + A`: Select all visible media
- Arrow keys: Navigate media selection, where appropriate

Respect platform conventions and avoid conflicts with current application shortcuts.

### Accessibility

Ensure:

- Accessible button labels
- Visible focus indicators
- Appropriate semantic roles
- Keyboard operability
- Sufficient text contrast
- Reduced-motion preference support
- Screen-reader-friendly status messages where supported

---

# 15. Performance Requirements

The new library must remain responsive with substantial media collections.

Design for:

- 100 files
- 1,000 files
- 10,000+ files

Use virtualization or incremental rendering when appropriate.

Optimize:

- Thumbnail loading and caching
- Filtering and sorting
- Metadata updates
- Component rerenders
- Scroll performance
- Memory consumption

Do not fetch or decode every thumbnail simultaneously.

Avoid loading full media files simply to populate the UI.

Use stable keys and cached metadata.

The UI must not block while download jobs are active or background library indexing is running.

---

# 16. Responsive Desktop Behavior

The mockup depicts a spacious desktop window.

Implement appropriate layout adaptations.

### Wide Window

- Expanded navigation sidebar
- Multiple grid columns
- Persistent optional details panel

### Medium Window

- Compact sidebar
- Fewer grid columns
- Collapsible details inspector

### Narrow Window

- Icon-only or hidden sidebar
- Simplified toolbar
- Appropriate media card sizing
- Details panel presented as an overlay, drawer, or full-width view

Avoid horizontal scrolling of the overall application.

Do not hide essential functionality at smaller window sizes.

---

# 17. Implementation Strategy

Execute the work in logical phases.

## Phase 1 — Discovery and Design Foundation

- Inspect repository architecture.
- Identify reusable components.
- Define library requirements.
- Establish design tokens.
- Create a durable implementation roadmap.
- Identify existing tests and CI requirements.

## Phase 2 — Layout and Visual System

- Implement new library shell.
- Refine navigation.
- Build responsive sidebar.
- Implement library overview.
- Build card components.
- Implement grid/list switching.

## Phase 3 — Data Integration

- Connect the redesigned UI to actual media records.
- Normalize library data where necessary.
- Preserve job-to-file relationships.
- Implement accurate metadata handling.
- Support missing files and thumbnail fallbacks.

## Phase 4 — Media Interaction

- Implement selection.
- Implement details inspector.
- Connect playback.
- Connect file actions.
- Implement context menus.
- Add multi-selection and safe bulk operations.

## Phase 5 — Search and Organization

- Implement search.
- Implement filters and sorting.
- Add channel and playlist browsing.
- Implement favorites.
- Implement collections and persistence where feasible.

## Phase 6 — Quality and Performance

- Add keyboard interactions.
- Improve empty and loading states.
- Optimize large-library rendering.
- Verify accessibility.
- Test application responsiveness.
- Run automated tests and platform builds.
- Fix any regressions.

Work through these phases sequentially while maintaining a usable application.

**Do not stop after producing the roadmap. Continue implementation.**

Keep the roadmap updated with completed work, outstanding items, and verification results.

---

# 18. Code Quality Requirements

Follow existing repository conventions.

Prefer:

- Reusable presentation components
- Clear separation of UI and data operations
- Typed models where supported
- Centralized styling tokens
- Small, testable functions
- Minimal external dependencies
- Predictable state management
- Explicit error handling

Avoid:

- Duplicating library business logic
- Hard-coded mock data in production
- Monolithic components
- Unnecessary architectural rewrites
- Breaking persisted user preferences
- Introducing dead or decorative controls
- Overcomplicated animation systems

When adding dependencies, justify their necessity and check compatibility with existing platforms and packaging.

---

# 19. Testing and Validation

Add or update automated tests covering:

### Library Data

- Correct media indexing
- Multiple files per job
- Metadata mapping
- Missing files
- Duplicate prevention
- Favorites persistence
- Collection persistence

### Library UI

- Grid/list switching
- Search results
- Filter combinations
- Sorting behavior
- Media selection
- Details inspector updates
- Empty states
- Keyboard navigation

### File Operations

- Reveal/open folder
- Copy path
- Delete confirmation
- Delete cancellation
- Partial deletion failure
- Remove-from-library behavior
- Multi-selection actions

### Regression Testing

Verify that the redesign does not break:

- Download queue
- Active downloads
- Download history
- Add Links
- Application settings
- Existing output directories
- Video/audio downloading
- Playlist downloading
- Platform-specific integrations

Run the repository's existing formatting, linting, type checking, and test suites.

Run available build workflows and resolve failures introduced by the changes.

Do not report tests as passing unless they were actually executed.

---

# 20. Visual Acceptance Criteria

Compare the implemented interface against the attached mockup.

The finished Library should have:

- A premium, cohesive dark theme.
- Clear media-first visual hierarchy.
- Attractive thumbnail-centric cards.
- A functional collapsible sidebar.
- Responsive grid and list views.
- A powerful search/filter toolbar.
- A contextual media inspector.
- Logical channel/playlist organization.
- Effective video and audio presentation.
- Subtle, polished interactions.
- Consistent typography, icons, and spacing.
- No overlapping, truncated, or misaligned controls at supported window sizes.

The visual reference should guide the overall design, but functional usability and correctness take priority over literal pixel matching.

Use real application data in final validation screenshots.

---

# 21. Git and Delivery Requirements

Create a dedicated feature branch following existing repository naming conventions.

Suggested branch:

`feature/media-library-v2`

Commit work in logical units corresponding to implementation milestones.

Preserve a durable implementation roadmap in the repository.

Update relevant documentation.

Open a pull request against the appropriate default branch when the implementation is ready for review.

The pull request must include:

1. Summary of the redesign.
2. Architectural changes.
3. New functionality.
4. Screenshots of the implemented Grid and List views.
5. Screenshots of the media inspector.
6. Testing performed and actual results.
7. Known limitations.
8. Remaining enhancement opportunities.
9. CI status.

Do not merge automatically unless explicitly instructed.

# 22. Definition of Done

This project is complete when:

- The old job-centric Library UI has been replaced by the redesigned media-first experience.
- Grid and List views work against actual library records.
- The media inspector shows accurate metadata.
- Existing playback and file operations remain functional.
- Search, sorting, and filters work.
- Audio files receive appropriate media-specific presentation.
- Favorites and implemented collections persist reliably.
- Existing user data remains accessible.
- The UI remains responsive for large libraries.
- Desktop layouts render correctly at typical window sizes.
- Relevant automated tests pass.
- Supported build pipelines pass or any environment-dependent limitations are documented.
- Documentation and roadmap reflect the completed implementation.
- A reviewable PR is available.

**Final directive:** Build this as a cohesive Media Library 2.0 experience, not a collection of disconnected visual enhancements. Prioritize production-quality implementation, architectural compatibility, interaction design, and visual polish. Use the supplied mockup as the design target, make reasonable decisions based on the repository, and implement the complete experience without stopping for routine confirmation.

Where existing architecture imposes constraints, use the most reliable solution and document any deviation from the visual reference.