#!/usr/bin/python3

import gi
gi.require_version('Gtk', '3.0')
from gi.repository import Gtk, Gdk, GLib
import sys
import os
import datetime
import dateutil.parser
from enum import Enum

from background import Background

COLOR_DEFAULT = '#3f3'
COLOR_CHANGED = '#88f'

class State(Enum):
    none     = 0
    scanning = 1
    scanned  = 2
    applying = 3
    applied  = 4

class ListBoxRowJob(Gtk.ListBoxRow):
    def __init__(self, data):
        super(Gtk.ListBoxRow, self).__init__()
        self.data = data
        self.add(Gtk.Label('abc'))

class Dummy:
    pass

def format_mode(fileType, mode, usedMode):
    s = fileType if fileType != 'f' else '-' # use '-' for regular files
    for ugo in range(3):
        for rwx in range(3):
            bit = (2-ugo)*3 + (2-rwx)
            if ~usedMode & (1 << bit):
                s += '?'
            elif not mode & (1 << bit):
                s += '-'
            else:
                s += 'rwx'[rwx]
    return s

def format_mtime(mtime):
    dt = dateutil.parser.parse(mtime)
    dt.replace(tzinfo=None) # move to local timezone
    return dt.strftime('%Y-%m-%d %H:%M:%S')

# http://stackoverflow.com/questions/1094841/reusable-library-to-get-human-readable-version-of-file-size#1094933
def format_size(num, suffix='B'):
    for unit in ['','K','M','G','T','P','E','Z']:
        if abs(num) < 10.0:
            return "%.1f%s%s" % (num, unit, suffix)
        elif abs(num) < 1024.0:
            return "%.0f%s%s" % (num, unit, suffix)
        num /= 1024.0
    return "%.1f%s%s" % (num, 'Y', suffix)


class Main(Gtk.Window):
    state = State.none

    def __init__(self, root1, root2):
        self.root1 = root1
        self.root2 = root2

        Gtk.Window.__init__(self, title='DTSync')
        self.set_name('DTSync')
        self.resize(1000, 600)
        self.setup_window()
        self.connect('delete-event', self.on_quit)

        self.load_style()

        self.background = Background(self)

        GLib.idle_add(self.scan)

    def setup_window(self):
        self.connect('key-press-event', self.on_keypress)

        box_outer = Gtk.Box(orientation=Gtk.Orientation.VERTICAL, spacing=6)
        self.add(box_outer)

        toolbar = Gtk.Box(spacing=6)
        box_outer.pack_start(toolbar, False, False, 0)

        self.btnScan = Gtk.Button(label='Scan')
        self.btnScan.connect('clicked', self.on_scan)
        toolbar.pack_start(self.btnScan, False, False, 0)

        self.btnApply = Gtk.Button(label='Apply')
        self.btnApply.connect('clicked', self.on_apply)
        toolbar.pack_start(self.btnApply, False, False, 0)

        separator = Gtk.Separator(orientation=Gtk.Orientation.VERTICAL)
        toolbar.pack_start(separator, False, False, 0)

        self.btnLeft = Gtk.Button(label='←')
        self.btnLeft.connect('clicked', self.on_left)
        toolbar.pack_start(self.btnLeft, False, False, 0)

        self.btnSkip = Gtk.Button(label='?')
        self.btnSkip.connect('clicked', self.on_skip)
        toolbar.pack_start(self.btnSkip, False, False, 0)

        self.btnRight = Gtk.Button(label='→')
        self.btnRight.connect('clicked', self.on_right)
        toolbar.pack_start(self.btnRight, False, False, 0)

        rootsbox = Gtk.Label('Dir 1: %s\nDir 2: %s' % (self.root1, self.root2))
        toolbar.pack_end(rootsbox, False, False, 0)

        self.store = Gtk.ListStore(int, str, str, str, str, str, str)

        scrolled_tree = Gtk.ScrolledWindow()
        box_outer.pack_start(scrolled_tree, True, True, 0)

        self.tree = Gtk.TreeView(self.store)
        self.tree.get_selection().set_mode(Gtk.SelectionMode.MULTIPLE)
        self.tree.set_rubber_banding(True)
        self.tree.set_search_column(4) # doesn't do anything
        self.tree.set_enable_search(False)
        self.tree.connect('cursor-changed', self.on_cursor_changed)
        self.tree.get_selection().connect('changed', self.on_tree_change)
        scrolled_tree.add(self.tree)

        self.tree.append_column(Gtk.TreeViewColumn('File 1', Gtk.CellRendererText(), text=1))
        self.tree.append_column(Gtk.TreeViewColumn('Action', Gtk.CellRendererText(), text=2, foreground=3))
        self.tree.append_column(Gtk.TreeViewColumn('File 2', Gtk.CellRendererText(), text=4))
        self.tree.append_column(Gtk.TreeViewColumn('Status', Gtk.CellRendererText(), text=5))
        self.tree.append_column(Gtk.TreeViewColumn('Path', Gtk.CellRendererText(), text=6))

        self.bottombar = Gtk.Stack()
        box_outer.pack_start(self.bottombar, False, False, 0)

        self.infobox = Gtk.Label('')
        self.infobox.set_selectable(True)
        self.infobox.set_name('infobox')
        self.infobox.set_alignment(0, 0)
        self.bottombar.add(self.infobox)

        self.scanbox = Gtk.Grid()
        self.bottombar.add(self.scanbox)

        self.scanbar = [Dummy(), Dummy()]
        for i in range(2):
            progress = Gtk.ProgressBar()
            self.scanbox.attach(progress, 0, i, 1, 1)
            self.scanbar[i].progress = progress

            path = Gtk.Label('')
            path.set_alignment(0, 0.5)
            path.set_selectable(True)
            self.scanbox.attach(path, 1, i, 1, 1)
            self.scanbar[i].path = path
        self.reset_scanbox()

        self.applybox = Gtk.Box()
        self.bottombar.add(self.applybox)

        self.apply_progress = Gtk.ProgressBar()
        self.applybox.pack_start(self.apply_progress, False, False, 0)

        self.apply_text = Gtk.Label('')
        self.apply_text.set_alignment(0, 1)
        self.applybox.pack_start(self.apply_text, False, False, 0)

        self.reset_applybox()

    def load_style(self):
        path = os.path.join(os.path.dirname(os.path.realpath(__file__)), 'style.css')
        style_provider = Gtk.CssProvider()
        style_provider.load_from_path(path)
        Gtk.StyleContext.add_provider_for_screen(
            Gdk.Screen.get_default(),
            style_provider,
            Gtk.STYLE_PROVIDER_PRIORITY_APPLICATION
        )

    def reset_scanbox(self):
        for i in range(2):
            self.scanbar[i].progress.set_fraction(0)
            self.scanbar[i].path.set_text('(loading...)')

    def reset_infobox(self):
        self.infobox.set_text('')

    def reset_applybox(self):
        self.apply_progress.set_fraction(0)
        self.apply_text.set_text('')

    def get_jobs(self):
        model, paths = self.tree.get_selection().get_selected_rows()
        jobs = []
        for path in paths:
            jobs.append(path.get_indices()[0])
        return jobs

    def move_down(self):
        if len(self.get_jobs()) > 1:
            return
        path, column = self.tree.get_cursor()
        index = path.get_indices()[0]
        index += 1 # go down one
        if index >= len(self.jobs):
            # can't move further down
            return
        self.tree.set_cursor(Gtk.TreePath.new_from_indices([index]), None, False)

    def on_quit(self, widget, data):
        self.background.quit()

    def on_keypress(self, widget, event):
        if event.string == '<':
            self.on_left()
        elif event.string == '>':
            self.on_right()
        elif event.string == '/':
            self.on_skip()
        elif event.string == 'r':
            self.on_scan()
        elif event.string == 'g':
            self.on_apply()

    def on_scan(self, widget=None):
        if self.state not in [State.scanned, State.applied]:
            return
        self.scan()

    def on_apply(self, widget=None):
        if self.state != State.scanned:
            return
        self.apply()

    def on_left(self, widget=None):
        self.background.jobs_direction(self.get_jobs(), -1)
        self.move_down()

    def on_skip(self, widget=None):
        self.background.jobs_direction(self.get_jobs(), 0)
        self.move_down()

    def on_right(self, widget=None):
        self.background.jobs_direction(self.get_jobs(), 1)
        self.move_down()

    def on_cursor_changed(self, tree_view):
        path, column = self.tree.get_cursor()
        if path is None:
            return
        job = self.jobs[path.get_indices()[0]]
        info = []
        for i in range(2):
            side = job['sides'][i]
            status = side['status'] or '-'
            line = 'File {}: {:12}'.format(i+1, status)
            metadata = side['metadata']
            if metadata is None:
                pass
            elif metadata['fileType'] == 'd':
                line += ' {:19}                      {}'.format(
                            format_mtime(metadata['mtime']),
                            format_mode(metadata['fileType'], metadata['mode'], metadata['usedMode']))
            else:
                size = '{} ({})'.format(format_size(metadata['size']), metadata['size'])
                line += ' {:19}   {:<18} {}'.format(
                            format_mtime(metadata['mtime']), size,
                            format_mode(metadata['fileType'], metadata['mode'], metadata['usedMode']))
            info.append(line)
        self.infobox.set_text('\n'.join(info))

    def on_tree_change(self, widget):
        may_change = self.state == State.scanned and len(self.get_jobs()) > 0
        self.btnLeft.set_sensitive(may_change)
        self.btnSkip.set_sensitive(may_change)
        self.btnRight.set_sensitive(may_change)

    def on_error(self, data):
        dialog = Gtk.MessageDialog(self, 0, Gtk.MessageType.ERROR,
                                   Gtk.ButtonsType.OK, data['title'])
        dialog.format_secondary_text(data['message'])
        dialog.run()
        dialog.destroy()
        sys.exit(1)

    def on_job_update(self, jobs):
        for index, job in jobs.items():
            self.jobs[index] = job
            iter = self.tree.get_model().iter_nth_child(None, index)
            self.store.set(iter, [0, 1, 2, 3, 4, 5, 6], self.job_values(index))

    def scan(self):
        if self.state not in [State.none, State.scanned, State.applied]:
            raise RuntimeError('scan() in invalid state')

        self.set_state(State.scanning)
        self.bottombar.set_visible_child(self.scanbox)
        self.reset_applybox()
        self.store.clear()
        self.infobox.set_text('')
        self.background.scan()

    def on_scan_progress(self, progresses):
        for i in range(2):
            progress = progresses[i]

            if progress is None:
                self.scanbar[i].progress.set_fraction(0)
                self.scanbar[i].path.set_text('(loading...)')
            else:
                if progress['total'] == 0:
                    self.scanbar[i].progress.pulse()
                else:
                    self.scanbar[i].progress.set_fraction(progress['done'] / progress['total'])
                self.scanbar[i].path.set_text(progress['path'])

    def on_scan_finished(self, result):
        self.set_state(State.scanned)
        self.bottombar.set_visible_child(self.infobox)
        self.reset_scanbox()

        self.jobs = result['jobs']
        self.store.clear()
        for i in range(len(self.jobs)):
            self.store.append(self.job_values(i))
        self.tree.grab_focus()

        if len(self.jobs) == 0:
            self.infobox.set_text('No changes to apply.')

    def apply(self):
        if self.state != State.scanned:
            raise RuntimeError('apply() in invalid state')

        if not self.jobs:
            self.infobox.set_text('Nothing to apply.')
        else:
            self.bottombar.set_visible_child(self.applybox)
            self.set_state(State.applying)
            self.background.apply()

    def on_apply_progress(self, progress):
        self.apply_progress.set_fraction(progress['totalProgress'])
        if progress['state'] == 'saving-status':
            self.apply_text.set_text('Saving tree state...')
            return

        iter = self.tree.get_model().iter_nth_child(None, progress['job'])
        if progress['state'] == 'starting':
            state = '0%'
        elif progress['state'] == 'progress':
            state = '{}%'.format(int(round((progress['jobProgress']*100))))
        elif progress['state'] == 'finished':
            state = '✓'
        else:
            print('unknown state:', progress['state'])
            state = '?'
        self.store.set_value(iter, 5, state)

    def on_apply_finished(self, result):
        self.set_state(State.applied)
        if result['error'] > 0:
            self.infobox.set_text('Finished with errors: %d applied, %d errors.' % (result['applied'], result['error']))
        elif result['applied'] == 0:
            self.infobox.set_text('No changes propagated.')
        else:
            self.infobox.set_text('Done, %d applied (no errors).' % result['applied'])
        self.bottombar.set_visible_child(self.infobox)
        self.reset_applybox()

    def set_state(self, state):
        self.state = state

        # update buttons
        self.btnScan.set_sensitive(state in [State.scanned, State.applied])
        self.btnApply.set_sensitive(state in [State.scanned])
        self.btnLeft.set_sensitive(False)
        self.btnSkip.set_sensitive(False)
        self.btnRight.set_sensitive(False)

    def job_values(self, i):
        job = self.jobs[i]
        if job['direction'] == job['origDirection']:
            stateColor = COLOR_DEFAULT
        else:
            stateColor = COLOR_CHANGED
        return [i,
                job['sides'][0]['status'],
                {-1: '←', 0: '?', 1: '→'}.get(job['direction'], '!!!'),
                stateColor,
                job['sides'][1]['status'],
                '',
                job['path']]

def main():
    if len(sys.argv) < 3:
        print('need at least 2 arguments: root1 and root2')
        return
    win = Main(sys.argv[1], sys.argv[2])
    win.connect('delete-event', Gtk.main_quit)
    win.show_all()
    Gtk.main()

if __name__ == '__main__':
    import signal
    signal.signal(signal.SIGINT, signal.SIG_DFL)
    main()
