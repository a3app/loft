# Списки loft

[English](README.md) · **Русский** · [中文](README_zh.md)

Именованные файлы в формате Keenetic / loft-routes, которые клиент для macOS может привязать к зоне. Актуальные списки и каталог для кнопки «Списки» лежат в [a3app/loft-routes](https://github.com/a3app/loft-routes). Файлы здесь — встроенные заглушки.

```
https://raw.githubusercontent.com/a3app/loft-routes/main/catalog.json
https://raw.githubusercontent.com/a3app/loft-routes/main/global.bat
https://raw.githubusercontent.com/a3app/loft-routes/main/ru.bat
https://raw.githubusercontent.com/a3app/loft-routes/main/cn.bat
https://raw.githubusercontent.com/a3app/loft-routes/main/ir.bat
https://raw.githubusercontent.com/a3app/loft-routes/main/kz.bat
https://raw.githubusercontent.com/a3app/loft-routes/main/by.bat
```

У зоны один URL. Направление задаётся у зоны, а не у файла: через лофт, мимо лофта или блокировать (встроенная зона Block, списка по умолчанию нет). `REM zone:` — подсказка при импорте.

Домены сервиса записываются отдельной строкой с тем же именем в комментарии: `REM domain: youtube.com googlevideo.com :: rem YouTube`. Домен включает поддомены. Клиент без поддержки доменов эти строки пропускает.

| Файл | Типичная зона |
|------|----------------|
| `global.bat` | Global (через лофт) |
| `ru.bat` | Local (мимо лофта) |
| `cn.bat` | Local (мимо лофта) |
| `ir.bat` | Local (мимо лофта) |
| `kz.bat` | Local (мимо лофта) |
| `by.bat` | Local (мимо лофта) |
