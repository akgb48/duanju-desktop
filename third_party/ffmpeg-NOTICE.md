# FFmpeg optional native decoder

The application downloads the pinned LGPL shared build on first use:

- FFmpeg 8.1.3, revision a35157abb9
- Build and corresponding source: https://github.com/FFmpeg-Builds/FFmpeg-Builds/releases/tag/autobuild-2026-11-09-12-42
- Build scripts and configuration: https://github.com/FFmpeg-Builds/FFmpeg-Builds
- Project and licensing: https://ffmpeg.org/legal.html
- LGPL text: https://www.gnu.org/licenses/old-licenses/lgpl-2.1.html

Archive: `ffmpeg-n8.1.3-34-ga35157abb9-win64-lgpl-shared-8.1.zip`

SHA256: `5e1b1ee3d9f85c2c2dedbe9db0036b9232f1f9bdd1a5e644fc7128e4bfcb6877`

FFmpeg runs as a separate process. Its executable, shared libraries and original
LICENSE.txt are stored together in the user's codec cache. They are not linked
into the application executable. No modifications to FFmpeg are made. The
application's MIT license does not apply to these components. Users may replace
the runtime through a source build; the automatic downloader verifies its own
pinned distribution for consistency.
