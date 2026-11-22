# SPDX-License-Identifier: AGPL-3.0-or-later
"""Build docs/img/avito-monitor-overview.gif from the screenshots in docs/img.

python docs/media/build_readme_gif.py   (needs Pillow)

Each frame is one screen of the panel with a caption under it. The frames are
saved screenshots, so the GIF shows what the screens look like and nothing
about how fast anything runs.
"""
import pathlib
from PIL import Image, ImageDraw, ImageFont

IMG = pathlib.Path(__file__).resolve().parent.parent / 'img'
WIDTH = 960
BAR = 64
FONT = 'C:/Windows/Fonts/segoeui.ttf'
FRAMES = [
    ('watches', 'Наблюдения: поиск или магазин, интервал и бюджет запросов'),
    ('results', 'Результаты: что сейчас в выдаче, с фильтрами и выгрузкой'),
    ('history', 'История: цена объявления по всем его перевыкладкам'),
    ('reposts', 'Перепубликации: что с чем связано и на каком основании'),
    ('rules-log', 'Правила: что отправлено в Telegram и о чём промолчали'),
    ('health', 'Здоровье сбора: чем разобраны страницы и что отвечал сайт'),
    ('channels', 'Прокси: свой адрес, списки, ротация и шлюзы VPN BlankTrail'),
]


def font(size):
    try:
        return ImageFont.truetype(FONT, size)
    except OSError:
        return ImageFont.truetype('DejaVuSans.ttf', size)


def frame(name, caption):
    shot = Image.open(IMG / f'{name}.png').convert('RGB')
    shot = shot.crop((0, 0, shot.width, min(shot.height, 900)))
    h = round(shot.height * WIDTH / shot.width)
    shot = shot.resize((WIDTH, h), Image.LANCZOS)
    page = Image.new('RGB', (WIDTH, 600 + BAR), (246, 247, 249))
    page.paste(shot.crop((0, 0, WIDTH, min(h, 600))), (0, 0))
    d = ImageDraw.Draw(page)
    d.rectangle((0, 600, WIDTH, 600 + BAR), fill=(36, 36, 62))
    d.rectangle((0, 600, 6, 600 + BAR), fill=(139, 44, 245))
    f = font(24)
    w = d.textlength(caption, font=f)
    d.text(((WIDTH - w) / 2, 600 + (BAR - 30) / 2), caption, font=f, fill=(255, 255, 255))
    return page.quantize(colors=128, method=Image.Quantize.MEDIANCUT)


def main():
    frames = [frame(n, c) for n, c in FRAMES]
    out = IMG / 'avito-monitor-overview.gif'
    frames[0].save(out, save_all=True, append_images=frames[1:], duration=3200, loop=0, optimize=True)
    print(out, round(out.stat().st_size / 1024), 'KB')


if __name__ == '__main__':
    main()
