import http.server
import pathlib
import ssl
import sys
import time
import threading

root = pathlib.Path(sys.argv[1])
class Handler(http.server.BaseHTTPRequestHandler):
    def do_GET(self):
        if self.path == '/generate_204':
            self.send_response(204)
            self.end_headers()
            return
        body = (root / 'body').read_bytes()
        if self.path == '/sub2':
            body = b'#profile-title: Second VPN\n' + body
        (root / 'request-started').write_text('yes')
        if (root / 'delay').exists():
            time.sleep(float((root / 'delay').read_text()))
        self.send_response(200)
        if self.path != "/sub2":
            self.send_header("Profile-Title", "base64:Q2xvdWQgVlBO")
        self.send_header('Subscription-Userinfo', 'upload=1073741824; download=2147483648; total=10737418240; expire=1798761600')
        self.send_header('Content-Length', str(len(body)))
        self.end_headers()
        self.wfile.write(body)
    def log_message(self, *args):
        pass
server = http.server.ThreadingHTTPServer(('0.0.0.0', 8443), Handler)
context = ssl.SSLContext(ssl.PROTOCOL_TLS_SERVER)
context.set_alpn_protocols(["http/1.1"])
context.load_cert_chain(root / 'cert.pem', root / 'key.pem')
server.socket = context.wrap_socket(server.socket, server_side=True)
reality_target = http.server.ThreadingHTTPServer(('0.0.0.0', 8444), Handler)
target_context = ssl.SSLContext(ssl.PROTOCOL_TLS_SERVER)
target_context.load_cert_chain(root / 'cert.pem', root / 'key.pem')
target_context.set_alpn_protocols(["h2", "http/1.1"])
reality_target.socket = target_context.wrap_socket(reality_target.socket, server_side=True)
threading.Thread(target=reality_target.serve_forever, daemon=True).start()
server.serve_forever()
