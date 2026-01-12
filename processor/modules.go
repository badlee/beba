package processor

// autoload modules

import (
	_ "http-server/modules/console"
	_ "http-server/modules/cookies"
	_ "http-server/modules/db"
	_ "http-server/modules/storage"
	// TODO: add fs modules
	// TODO: add fetch modules : http://, https://, ftp://, ftps://, sftp://, file:// (limited to the current directory)
	// TODO: add net modules : TCP, UDP socket client
	// TODO: add path modules : filepath manipulation
	// TODO: add process modules : like nodejs controll current process
	// TODO: add unicode modules
	// TODO: add SSE, WebSocket modules : Client for SSE and WebSocket streams
	// TODO: add zlib modules : compression and decompression
	// TODO: add crypto modules : encryption and decryption
)
