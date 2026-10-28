"""Integration controller, run in the fixture network namespace."""
import http.client
import json
import pathlib
import sys
import time
import urllib.parse

root = pathlib.Path(sys.argv[1])
phase = sys.argv[2]
cookie = ''
csrf = ''

def call(method, path, body=None, expected=200, auth=True):
    global cookie
    connection = http.client.HTTPConnection('10.200.0.2', 8080, timeout=50,
                                            source_address=('198.51.100.20', 0))
    headers = {'Content-Type': 'application/json', 'Origin': 'http://10.200.0.2:8080'}
    if auth:
        headers.update({'Cookie': cookie, 'X-CSRF-Token': csrf})
    connection.request(method, path, body=json.dumps(body or {}) if method != 'GET' else None,
                       headers=headers)
    response = connection.getresponse()
    data = response.read()
    if response.getheader('Set-Cookie'):
        cookie = response.getheader('Set-Cookie').split(';')[0]
    assert response.status == expected, (path, response.status, data[:200])
    connection.close()
    return json.loads(data) if data else {}

for _ in range(30):
    try:
        call('GET', '/healthz', auth=False)
        break
    except (OSError, AssertionError):
        time.sleep(.2)
else:
    raise AssertionError('Panel unavailable from off-subnet peer')
connection = http.client.HTTPConnection('10.200.0.2', 8080, timeout=50,
                                         source_address=('198.51.100.20', 0))
connection.request('POST', '/login', body=urllib.parse.urlencode({'username': 'xray-ui', 'password': '1234'}),
                   headers={'Content-Type': 'application/x-www-form-urlencoded', 'Origin': 'http://10.200.0.2:8080'})
response = connection.getresponse()
assert response.status == 303 and response.getheader('Location') == '/', 'native sign-in failed'
cookie = response.getheader('Set-Cookie').split(';')[0]
response.read()
connection.close()
initial = call('GET', '/api/state')
assert initial['coreVersion'] == '26.9.9', 'installed Xray version missing'
csrf = initial['csrf']
if phase in ('first', 'source-change'):
    for _ in range(120):
        state = call('GET', '/api/state')
        if state['running'] and not state.get('probing'):
            break
        time.sleep(.2)
    else:
        raise AssertionError(('automatic startup failed', state['lastError']))

if phase == 'first':
    call('GET', '/api/state', expected=401, auth=False)
    state = call('GET', '/api/state')
    assert len(state['subscriptions']) == 1
    assert state['subscriptions'][0]['name'] == 'Cloud VPN', 'provider title was not decoded'
    sub = state['subscriptions'][0]['id']
    call('POST', '/api/subscriptions', {'name': 'Forbidden', 'url': 'https://evil.example/sub'}, expected=404)
    call('POST', f'/api/subscriptions/{sub}/refresh')
    state = call('GET', '/api/state')
    assert state['running'], 'startup failed to connect automatically'
    assert state['subscriptions'][0]['nodes'][0]['probe']['reachable']
    call('POST', '/api/probe')
    assert call('GET', '/api/state')['running'], 'probe interrupted active VPN'
    node = state['subscriptions'][0]['nodes'][0]
    assert state['subscriptions'][0]['skipped'] == 2
    call('POST', '/api/activate', {'subscriptionID': sub, 'nodeID': node['id']})
    assert call('GET', '/api/state')['running']
    call('POST', '/api/xray/restart')
    call('POST', '/api/xray/stop')
    call('POST', f'/api/subscriptions/{sub}/refresh')
    state = call('GET', '/api/state')
    assert not state['running'] and not state['active']['enabled'], 'refresh undid Stop'
    (root / 'sub-id').write_text(sub)
    print('panel lifecycle and off-subnet routing passed', flush=True)
elif phase == 'second':
    state = call('GET', '/api/state')
    assert not state['running'] and not state['active']['enabled'], 'boot ignored Stop'
    sub = (root / 'sub-id').read_text()
    call('POST', '/api/xray/start')
    state = call('GET', '/api/state')
    node = state['subscriptions'][0]['nodes'][0]
    call('GET', f'/api/subscriptions/{sub}', expected=404)
    (root / 'body').write_text('<html>bad response</html>')
    call('POST', f'/api/subscriptions/{sub}/refresh', expected=400)
    state = call('GET', '/api/state')
    assert state['running'] and state['active']['node']['id'] == node['id']
    serialized = json.dumps(state)
    assert '11111111-1111-4111-8111-111111111111' not in serialized
    assert 'abcdefghijklmnopqrstuvwxyz0123456789ABCDEFG' not in serialized
    assert 'https://198.51.100.20:8443' not in serialized
    call('POST', '/api/logout')
    call('GET', '/api/state', expected=401)
    print('persistence, read-only sources and secret redaction passed', flush=True)
elif phase == 'source-change':
    state = call('GET', '/api/state')
    assert state['active']['subscriptionID'] != (root / 'sub-id').read_text(), 'removed env source resumed old selection'
    assert len(state['subscriptions']) == 1
    sub = state['subscriptions'][0]['id']
    call('POST', f'/api/subscriptions/{sub}/refresh')
    state = call('GET', '/api/state')
    assert state['running'], 'new source was not connected automatically'
    node = state['subscriptions'][0]['nodes'][0]
    call('POST', '/api/activate', {'subscriptionID': sub, 'nodeID': node['id']})
    assert call('GET', '/api/state')['running']
    (root / 'sub-id').write_text(sub)
    print('environment source replacement passed', flush=True)
elif phase == 'slow':
    sub = (root / 'sub-id').read_text()
    (root / 'delay').write_text('30')
    call('POST', f'/api/subscriptions/{sub}/refresh', expected=400)
    print('download cancelled on shutdown', flush=True)
