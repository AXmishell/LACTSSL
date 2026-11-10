#!/usr/bin/env python3
"""根据 web/public/favicon.svg 生成多尺寸 favicon.ico。

仅依赖 Pillow，用于在无矢量栅格化工具的环境中重建浏览器图标。
用法：python3 scripts/gen-favicon.py
"""

import re
from pathlib import Path

from PIL import Image, ImageDraw

OUT = Path(__file__).resolve().parent.parent / "web" / "public" / "favicon.ico"

# 与 favicon.svg 保持一致的配色与几何比例（viewBox 0 0 64 64）。
# 蓝色渐变：浅蓝 -> 主色蓝 -> 深蓝。
GRADIENT_STOPS = (
    (0.0, (96, 165, 250)),
    (0.5, (37, 99, 235)),
    (1.0, (29, 78, 216)),
)
TEXT_COLOR = (255, 255, 255, 255)
TEXT_OFFSET_Y = -4.0  # SSL 字样相对路径基准上移，与 SVG 保持一致

# 与 favicon.svg 中 "SSL" 路径完全一致（由 Red Hat Text Bold 字形轮廓生成）。
SSL_PATH = (
    "M23.18 37.98Q22.04 37.98 21.01 37.54Q19.97 37.11 19.01 36.28L20.22 34.25"
    "Q21.23 35.06 21.93 35.4Q22.63 35.74 23.34 35.74Q23.85 35.74 24.22 35.58"
    "Q24.58 35.43 24.78 35.17Q24.98 34.9 24.98 34.55Q24.98 34.06 24.7 33.79"
    "Q24.42 33.53 23.72 33.38L21.67 32.97Q20.52 32.74 19.91 31.94Q19.3 31.14"
    " 19.3 29.96Q19.3 28.87 19.75 28.08Q20.21 27.29 21.07 26.86Q21.93 26.42"
    " 23.12 26.42Q24.24 26.42 25.29 26.82Q26.33 27.21 27.07 27.9L25.88 29.9"
    "Q25.18 29.27 24.47 28.97Q23.77 28.66 23 28.66Q22.53 28.66 22.2 28.8"
    "Q21.87 28.94 21.7 29.18Q21.53 29.42 21.53 29.72Q21.53 30.15 21.78 30.38"
    "Q22.04 30.62 22.62 30.74L24.54 31.13Q25.89 31.38 26.59 32.2Q27.29 33.02"
    " 27.29 34.3Q27.29 35.38 26.79 36.22Q26.29 37.05 25.37 37.51Q24.45 37.98"
    " 23.18 37.98Z M32.19 37.98Q31.05 37.98 30.02 37.54Q28.99 37.11 28.02 36.28"
    "L29.23 34.25Q30.24 35.06 30.94 35.4Q31.64 35.74 32.36 35.74Q32.87 35.74"
    " 33.23 35.58Q33.6 35.43 33.8 35.17Q34 34.9 34 34.55Q34 34.06 33.71 33.79"
    "Q33.43 33.53 32.73 33.38L30.68 32.97Q29.54 32.74 28.92 31.94Q28.31 31.14"
    " 28.31 29.96Q28.31 28.87 28.77 28.08Q29.22 27.29 30.08 26.86Q30.94 26.42"
    " 32.14 26.42Q33.25 26.42 34.3 26.82Q35.34 27.21 36.09 27.9L34.89 29.9"
    "Q34.19 29.27 33.49 28.97Q32.78 28.66 32.01 28.66Q31.55 28.66 31.22 28.8"
    "Q30.89 28.94 30.71 29.18Q30.54 29.42 30.54 29.72Q30.54 30.15 30.8 30.38"
    "Q31.05 30.62 31.63 30.74L33.55 31.13Q34.9 31.38 35.61 32.2Q36.31 33.02"
    " 36.31 34.3Q36.31 35.38 35.8 36.22Q35.3 37.05 34.38 37.51Q33.46 37.98"
    " 32.19 37.98Z M37.85 37.8V26.6H40.21V35.45H44.99V37.8Z"
)

SCALE = 16  # 超采样倍数


def lerp_color(t):
    t = min(1.0, max(0.0, t))
    for (a_t, a_c), (b_t, b_c) in zip(GRADIENT_STOPS, GRADIENT_STOPS[1:]):
        if t <= b_t:
            k = 0.0 if b_t == a_t else (t - a_t) / (b_t - a_t)
            return tuple(round(a + (b - a) * k) for a, b in zip(a_c, b_c))
    return GRADIENT_STOPS[-1][1]


def cubic(p0, p1, p2, p3, steps=48):
    pts = []
    for i in range(steps + 1):
        t = i / steps
        u = 1 - t
        pts.append(
            (
                u**3 * p0[0] + 3 * u * u * t * p1[0] + 3 * u * t * t * p2[0] + t**3 * p3[0],
                u**3 * p0[1] + 3 * u * u * t * p1[1] + 3 * u * t * t * p2[1] + t**3 * p3[1],
            )
        )
    return pts


def shield_points():
    pts = [(32, 10), (48, 16), (48, 30)]
    pts += cubic((48, 30), (48, 41), (41.2, 49.4), (32, 54))
    pts += cubic((32, 54), (22.8, 49.4), (16, 41), (16, 30))
    pts += [(16, 16)]
    return pts


def flatten_path(d, scale, steps=24):
    """把只含 M/L/Q/Z 的 SVG 路径展开为多边形顶点列表。"""
    tokens = re.findall(r"[MLQZHV]|-?\d+(?:\.\d+)?", d)
    polygons, poly, start, cur = [], None, None, None
    i = 0
    while i < len(tokens):
        cmd = tokens[i]
        i += 1
        if cmd == "M":
            x, y = float(tokens[i]) * scale, float(tokens[i + 1]) * scale
            i += 2
            poly, start, cur = [(x, y)], (x, y), (x, y)
        elif cmd == "L":
            x, y = float(tokens[i]) * scale, float(tokens[i + 1]) * scale
            i += 2
            poly.append((x, y))
            cur = (x, y)
        elif cmd == "H":
            x = float(tokens[i]) * scale
            i += 1
            poly.append((x, cur[1]))
            cur = (x, cur[1])
        elif cmd == "V":
            y = float(tokens[i]) * scale
            i += 1
            poly.append((cur[0], y))
            cur = (cur[0], y)
        elif cmd == "Q":
            cx, cy = float(tokens[i]) * scale, float(tokens[i + 1]) * scale
            x, y = float(tokens[i + 2]) * scale, float(tokens[i + 3]) * scale
            i += 4
            x0, y0 = cur
            for step in range(1, steps + 1):
                t = step / steps
                u = 1 - t
                poly.append(
                    (
                        u * u * x0 + 2 * u * t * cx + t * t * x,
                        u * u * y0 + 2 * u * t * cy + t * t * y,
                    )
                )
            cur = (x, y)
        elif cmd == "Z":
            poly.append(start)
            polygons.append(poly)
            poly, cur = None, start
        else:  # pragma: no cover
            raise ValueError(f"不支持的路径指令: {cmd}")
    return polygons


def gradient_image(size):
    # 低分辨率生成渐变后放大，避免逐像素处理超大画布。
    base = 64
    grad = Image.new("RGBA", (base, base))
    px = grad.load()
    dx, dy = 48 - 16, 54 - 10
    length_sq = dx * dx + dy * dy
    for y in range(base):
        for x in range(base):
            t = ((x - 16) * dx + (y - 10) * dy) / length_sq
            r, g, b = lerp_color(t)
            px[x, y] = (r, g, b, 255)
    return grad.resize((size, size), Image.BICUBIC)


def render(size):
    # 以 64 为设计基准，放大到目标尺寸后再降采样，获得平滑边缘。
    s = size * SCALE / 64
    canvas = size * SCALE

    shield = [(x * s, y * s) for x, y in shield_points()]
    mask = Image.new("L", (canvas, canvas), 0)
    ImageDraw.Draw(mask).polygon(shield, fill=255)

    transparent = Image.new("RGBA", (canvas, canvas), (0, 0, 0, 0))
    img = Image.composite(gradient_image(canvas), transparent, mask)
    draw = ImageDraw.Draw(img)

    for poly in flatten_path(SSL_PATH, s):
        draw.polygon([(x, y + TEXT_OFFSET_Y * s) for x, y in poly], fill=TEXT_COLOR)

    return img.resize((size, size), Image.LANCZOS)


def main():
    base = render(256)
    base.save(
        OUT,
        format="ICO",
        sizes=[(16, 16), (24, 24), (32, 32), (48, 48), (64, 64), (128, 128), (256, 256)],
    )
    print(f"已生成 {OUT} ({OUT.stat().st_size} 字节)")


if __name__ == "__main__":
    main()
