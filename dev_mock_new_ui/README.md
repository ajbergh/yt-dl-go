# UI reference mock

This directory contains the supplied visual and interaction mock used as a design reference for the production downloader in `../src`. It is a separate Vite project, not the application bundled into `youtube-downloader.exe`.

The mock uses fixture data and browser `localStorage`; it does not connect to the Go service or SQLite database. Its dependencies and scripts are isolated in this directory's `package.json`. For production setup, build, API, and database instructions, see the repository [README](../README.md), [frontend guide](../src/README.md), and [Go service guide](../src/server/README.md).
