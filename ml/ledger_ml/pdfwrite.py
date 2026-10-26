"""A minimal PDF writer: lines of monospaced text over as many A4 pages as they need.

Enough to produce a bank statement for the importer tests without a PDF library. The text is
ASCII (bank statements are), set in the standard Courier font, so nothing needs embedding.
"""

from __future__ import annotations

from pathlib import Path

LINES_PER_PAGE = 62


def _escape(s: str) -> str:
    return s.replace("\\", "\\\\").replace("(", "\\(").replace(")", "\\)")


def write(path: Path, lines: list[str]) -> None:
    pages = [lines[i : i + LINES_PER_PAGE] for i in range(0, max(len(lines), 1), LINES_PER_PAGE)]
    objects: list[bytes] = []

    def add(body: bytes) -> int:
        objects.append(body)
        return len(objects)

    # Courier is monospaced: every glyph is 600/1000 of the font size wide. Declaring the widths lets
    # readers place each character, as with any real statement.
    widths = b" ".join(b"600" for _ in range(32, 127))
    font = add(b"<< /Type /Font /Subtype /Type1 /BaseFont /Courier /Encoding /WinAnsiEncoding /FirstChar 32 /LastChar 126 /Widths ["
               + widths + b"] >>")
    pages_id = len(objects) + 1 + 2 * len(pages)  # the Pages object comes after every page and its content
    page_ids = []
    for page in pages:
        text = ["BT", "/F1 8 Tf", "10 TL", "30 810 Td"]
        for line in page:
            ascii_line = line.encode("ascii", "replace").decode("ascii")
            text.append(f"({_escape(ascii_line)}) Tj T*")
        text.append("ET")
        stream = "\n".join(text).encode("ascii")
        content = add(b"<< /Length %d >>\nstream\n" % len(stream) + stream + b"\nendstream")
        page_ids.append(
            add(b"<< /Type /Page /Parent %d 0 R /MediaBox [0 0 595 842] /Resources << /Font << /F1 %d 0 R >> >> /Contents %d 0 R >>"
                % (pages_id, font, content))
        )
    kids = " ".join(f"{p} 0 R" for p in page_ids).encode("ascii")
    assert add(b"<< /Type /Pages /Kids [" + kids + b"] /Count %d >>" % len(page_ids)) == pages_id
    catalog = add(b"<< /Type /Catalog /Pages %d 0 R >>" % pages_id)

    out = bytearray(b"%PDF-1.4\n%\xe2\xe3\xcf\xd3\n")
    offsets = []
    for i, body in enumerate(objects, start=1):
        offsets.append(len(out))
        out += b"%d 0 obj\n" % i + body + b"\nendobj\n"
    xref = len(out)
    out += b"xref\n0 %d\n0000000000 65535 f \n" % (len(objects) + 1)
    for off in offsets:
        out += b"%010d 00000 n \n" % off
    out += b"trailer\n<< /Size %d /Root %d 0 R >>\nstartxref\n%d\n%%%%EOF\n" % (len(objects) + 1, catalog, xref)
    Path(path).write_bytes(bytes(out))
