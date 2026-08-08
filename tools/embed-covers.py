#!/usr/bin/env python3
"""Embed cover art into albums under a music root.

Sources, in order: existing image files in the album dir, iTunes Search API,
MusicBrainz Cover Art Archive. Skips albums that already have embedded art.
"""
import os
import sys
import time
import urllib.request
import urllib.parse
import json

from mutagen.flac import FLAC, Picture
from mutagen.mp3 import MP3
from mutagen.id3 import APIC, ID3
from mutagen.mp4 import MP4, MP4Cover

ROOT = sys.argv[1] if len(sys.argv) > 1 else '/srv/immich/music'
MUSIC_EXT = {'.mp3', '.flac', '.m4a', '.aac', '.ogg', '.opus', '.wma'}
IMG_EXT = {'.jpg', '.jpeg', '.png', '.gif', '.bmp', '.webp'}
UA = {'User-Agent': 'AlbumArtTool/1.0 (vladimir.perovic@gmail.com)'}


def audio_files(d):
    return [f for f in os.listdir(d) if os.path.splitext(f)[1].lower() in MUSIC_EXT]


def load_id3(path):
    try:
        return ID3(path)
    except Exception:
        return ID3()


def get_tags(path):
    ext = os.path.splitext(path)[1].lower()
    if ext == '.mp3':
        t = load_id3(path)
        return t.get('TPE1'), t.get('TALB'), t.get('APIC')
    if ext == '.flac':
        f = FLAC(path)
        return f.get('artist'), f.get('album'), (f.pictures or [None])[0]
    if ext == '.m4a':
        m = MP4(path)
        art = None
        if 'covr' in m and m['covr']:
            art = m['covr'][0]
        return m.get('\xa9ART'), m.get('\xa9alb'), art
    return None, None, None


def has_art(path):
    _, _, art = get_tags(path)
    return art is not None


def existing_art_file(d):
    imgs = [f for f in os.listdir(d) if os.path.splitext(f)[1].lower() in IMG_EXT]
    if not imgs:
        return None
    for pref in ('folder', 'cover', 'front', 'albumart', 'artwork'):
        for f in imgs:
            if f.lower().startswith(pref):
                return os.path.join(d, f)
    return os.path.join(d, imgs[0])


def fetch(url, timeout=20):
    req = urllib.request.Request(url, headers=UA)
    with urllib.request.urlopen(req, timeout=timeout) as r:
        return r.read()


def art_from_itunes(artist, album):
    q = urllib.parse.urlencode({'term': f'{artist} {album}', 'media': 'music',
                                'entity': 'album', 'limit': 1})
    data = json.loads(fetch(f'https://itunes.apple.com/search?{q}'))
    for r in data.get('results', []):
        url = r.get('artworkUrl100')
        if url:
            return fetch(url.replace('100x100', '600x600'))
    return None


def art_from_musicbrainz(artist, album):
    q = urllib.parse.urlencode({'query': f'release:"{album}" AND artist:"{artist}"',
                                'fmt': 'json'})
    data = json.loads(fetch(f'https://musicbrainz.org/ws/2/release/?{q}'))
    for r in data.get('releases', [])[:5]:
        rg = r.get('release-group', {}).get('id')
        if not rg:
            continue
        try:
            return fetch(f'https://coverartarchive.org/release-group/{rg}/front-500')
        except Exception:
            time.sleep(1.1)
    return None


def embed(path, data):
    ext = os.path.splitext(path)[1].lower()
    try:
        if ext == '.mp3':
            t = load_id3(path)
            t.delall('APIC')
            t.add(APIC(encoding=3, mime='image/jpeg', type=3, desc='Cover', data=data))
            t.save(path)
        elif ext == '.flac':
            f = FLAC(path)
            f.clear_pictures()
            p = Picture()
            p.type = 3
            p.mime = 'image/jpeg'
            p.data = data
            f.add_picture(p)
            f.save()
        elif ext == '.m4a':
            m = MP4(path)
            m['covr'] = [MP4Cover(data, imageformat=MP4Cover.FORMAT_JPEG)]
            m.save()
        else:
            return False
    except Exception:
        return False
    return True


def name_from_dir(d):
    base = os.path.basename(d)
    if ' - ' in base:
        artist, album = base.split(' - ', 1)
        return artist.strip(), album.strip()
    return '', base.strip()


def find_art(artist, album):
    data = None
    if artist or album:
        try:
            data = art_from_itunes(artist or album, album)
        except Exception as e:
            print(f'  itunes fail: {e}')
        if not data:
            time.sleep(1.1)
            try:
                data = art_from_musicbrainz(artist or album, album)
            except Exception as e:
                print(f'  mbz fail: {e}')
    return data


def embed_album(dirpath, files, first):
    local = existing_art_file(dirpath)
    if local:
        with open(local, 'rb') as fh:
            data = fh.read()
    else:
        artist, album, _ = get_tags(first)
        artist = str(artist or '').strip()
        album = str(album or '').strip()
        if not artist or not album:
            artist, album = name_from_dir(dirpath)
        data = find_art(artist, album)
        if not data:
            n_artist, n_album = name_from_dir(dirpath)
            if (n_artist, n_album) != (artist, album) and (n_artist or n_album):
                data = find_art(n_artist or n_album, n_album)
    if not data:
        return 'NO ART'
    ok = sum(1 for f in files if embed(os.path.join(dirpath, f), data))
    return f'{ok}/{len(files)} files' if ok == len(files) else f'PARTIAL {ok}/{len(files)}'


def process(root):
    total = embedded = skipped = failed = 0
    for dirpath, dirnames, filenames in os.walk(root):
        files = audio_files(dirpath)
        if not files:
            continue
        total += 1
        albums = {}
        for f in files:
            artist, album, _ = get_tags(os.path.join(dirpath, f))
            albums.setdefault((str(artist or '').strip(), str(album or '').strip()), []).append(f)
        if len(albums) == 1:
            first = os.path.join(dirpath, sorted(files)[0])
            if has_art(first):
                skipped += 1
                continue
            res = embed_album(dirpath, files, first)
            if res == 'NO ART':
                failed += 1
                print(f'NO ART: {dirpath}')
            else:
                embedded += 1
                print(f'OK: {dirpath} ({res})')
            continue
        per = 0
        for (artist, album), grp in albums.items():
            for f in grp:
                path = os.path.join(dirpath, f)
                if has_art(path):
                    skipped += 1
                    continue
                data = find_art(artist or album, album) if (artist or album) else None
                if not data:
                    n_artist, n_album = name_from_dir(dirpath)
                    if n_artist or n_album:
                        data = find_art(n_artist or n_album, n_album)
                if data and embed(path, data):
                    per += 1
                else:
                    failed += 1
        if per:
            embedded += 1
            print(f'OK: {dirpath} ({per}/{len(files)} files, per-file)')
        time.sleep(0.2)
    print(f'\nDONE: {total} dirs, {embedded} embedded, {skipped} skipped, {failed} failed')


if __name__ == '__main__':
    process(ROOT)
