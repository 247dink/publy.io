import threading
import subprocess
import time
import queue
import uuid
import socket
import logging

from unittest import TestCase
from os.path import dirname, join as pathjoin

import requests
from websockets.sync.client import connect


PUBLY_PATH = pathjoin(dirname(dirname(__file__)), 'publy.io')
CHANNEL_NAME = str(uuid.uuid4())


def get_free_port():
    s = socket.socket()
    s.bind(('', 0))
    try:
        return s.getsockname()[1]

    finally:
        s.close()


class Publy:
    def __init__(self):
        self.host = '127.0.0.1'
        self.port = get_free_port()
        self._p = subprocess.Popen(
            [PUBLY_PATH, '-h', self.host, '-p', str(self.port)],
        )
        self._wait_for_port()
        self._running = True
        self._event = threading.Event()
        self._queue = queue.Queue()
        self._t = threading.Thread(target=self._run, daemon=True)
        self._t.start()
        self._wait_for_ws()

    def _run(self):
        addr = f'ws://{self.host}:{self.port}/{CHANNEL_NAME}/'
        with connect(addr) as ws:
            self._event.set()
            while self._running:
                message = ws.recv()
                self._queue.put(message)

    def _wait_for_port(self):
        s = socket.socket()
        while True:
            try:
                s.connect(self.address())
                s.close()
                break

            except ConnectionRefusedError:
                time.sleep(0.1)


    def _wait_for_ws(self):
        assert self._event.wait(1.0), "Websocket client connection failed"

    def address(self):
        return (self.host, self.port)

    def dispatch(self, message, method='post'):
        kwargs = {}
        url = f'http://{self.host}:{self.port}/{CHANNEL_NAME}/'
        if method == 'get':
            url += f'?{message}'
        else:
            kwargs['data'] = message
        r = requests.request(method, url, **kwargs)
        r.raise_for_status()

    def stop(self):
        self._running = False
        self._p.terminate()


class BaseTestCase(TestCase):
    @classmethod
    def setUpClass(cls):
        cls.publy = Publy()

    @classmethod
    def tearDownClass(cls):
        cls.publy.stop()

    def assertReceived(self, message):
        try:
            recv = self.publy._queue.get(timeout=1.0)

        except queue.Empty:
            recv = None

        self.assertEqual(recv, message)


class PublyTestCase(BaseTestCase):
    def test_publy_post(self):
        self.publy.dispatch('PING')
        self.assertReceived('PING')

    def test_publy_get(self):
        self.publy.dispatch('PING', method='get')
        self.assertReceived('PING')
