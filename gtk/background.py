
import gi
gi.require_version('Gtk', '3.0')
from gi.repository import GLib

import subprocess
import queue
from threading import Thread, Event
import msgpack
import sys

class Background:
    def __init__(self, main):
        self.main = main
        self.willExit = False # expect the process to exit

        processThread = Thread(target=self.process, daemon=True)
        processThread.start()

        self.daemon = None
        self.daemonStarted = Event()
        self.sendQueue = queue.Queue(1)
        writerThread = Thread(target=self.writer, daemon=True)
        writerThread.start()

    def process(self):
        try:
            self.daemon = subprocess.Popen(['dtsync', '-output', 'msgpack', self.main.root1, self.main.root2], stdin=subprocess.PIPE, stdout=subprocess.PIPE, bufsize=0)
        except:
            raise
        finally:
            self.daemonStarted.set()
        unpacker = msgpack.Unpacker(self.daemon.stdout, encoding='utf-8')
        for msg in unpacker:
            if self.willExit:
                print('Expected the process to exit?')

            if msg['message'] == 'scan-progress':
                GLib.idle_add(self.main.on_scan_progress, msg['value'])
            elif msg['message'] == 'scan-finished':
                GLib.idle_add(self.main.on_scan_finished, msg['value'])
            elif msg['message'] == 'error':
                if msg['root'] == -1:
                    value = 'Error: {}\n\n{}'.format(msg['error'], msg['value'])
                else:
                    value = 'Error in root {}: {}\n\n{}'.format(msg['root'], msg['error'], msg['value'])
                GLib.idle_add(self.main.on_error, {'title': 'Error',
                                                   'message': value})
                self.willExit = True
            elif msg['message'] == 'job-update':
                GLib.idle_add(self.main.on_job_update, msg['value'])
            elif msg['message'] == 'apply-progress':
                GLib.idle_add(self.main.on_apply_progress, msg['value'])
            elif msg['message'] == 'apply-finished':
                GLib.idle_add(self.main.on_apply_finished, msg['value'])
            else:
                print('unknown message:', msg)

        if not self.willExit:
            GLib.idle_add(self.main.on_error, {'title': 'Process quit', 'message': 'Background dtsync process quit unexpectedly'})

    def quit(self):
        self.daemonStarted.wait()
        if self.daemon:
            self.daemon.terminate()
        self.willExit = True

    def writer(self):
        self.daemonStarted.wait()
        packer = msgpack.Packer()
        while True:
            message = self.sendQueue.get()
            buf = packer.pack(message)
            self.daemon.stdin.write(buf)

    def jobs_direction(self, jobs, direction):
        self.sendQueue.put({
            'command':   'job-direction',
            'jobs':      jobs,
            'direction': direction,
        })

    def scan(self):
        self.sendQueue.put({
            'command':   'scan',
        })

    def apply(self):
        self.sendQueue.put({
            'command':   'apply',
        })


