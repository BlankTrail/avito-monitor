<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->

# Как запустить на машине, на которую никто не входит

На своём ноутбуке ничего из этого не нужно: в панели есть галочка «Запускать при
входе в систему», и она пишет то, что для вашей системы правильно — запись в
реестре на Windows, `LaunchAgent` на macOS, пользовательский юнит `systemd` на
Linux. Всё это работает от вашего имени и без прав администратора.

Здесь — другой случай: сервер, на который никто не логинится. Такие юниты
программа себе **не ставит сама**, и это решение: программа, которая
устанавливает себе службу от root, просит права, которые ей больше нигде не
нужны.

## systemd

```bash
sudo install -m644 avitomon.service /etc/systemd/system/
sudo useradd --system --home /var/lib/avitomon --create-home avitomon
sudo install -m755 avitomon /usr/local/bin/
sudo systemctl daemon-reload
sudo systemctl enable --now avitomon
```

## launchd

`launchd` ничего не создаёт сам, а плист рассчитывает на служебного
пользователя `_avitomon` и на каталоги, которых на чистой машине нет. Раздел
systemd выше заводит своего пользователя через `useradd --create-home`; здесь
то же самое делается руками.

```bash
NEWUID=$(( $(dscl . -list /Users UniqueID | awk '$2 < 500 {print $2}' | sort -n | tail -1) + 1 ))
sudo dscl . -create /Groups/_avitomon PrimaryGroupID "$NEWUID"
sudo dscl . -create /Users/_avitomon UserShell /usr/bin/false
sudo dscl . -create /Users/_avitomon UniqueID "$NEWUID"
sudo dscl . -create /Users/_avitomon PrimaryGroupID "$NEWUID"
```

Идентификатор берётся на единицу больше самого большого служебного — это те,
что ниже 500. Взять первый попавшийся нельзя: занятый номер отдаст демон
чужому пользователю.

```bash
sudo mkdir -p /usr/local/var/avitomon /usr/local/var/log
sudo touch /usr/local/var/log/avitomon.log
sudo chown -R _avitomon:_avitomon /usr/local/var/avitomon /usr/local/var/log/avitomon.log
sudo install -m755 avitomon /usr/local/bin/
sudo install -m644 com.blanktrail.avitomon.plist /Library/LaunchDaemons/
sudo launchctl load -w /Library/LaunchDaemons/com.blanktrail.avitomon.plist
```

Проверить, что поднялся:

```bash
sudo launchctl print system/com.blanktrail.avitomon | head -20
```

## Панель

Она слушает `127.0.0.1` и без пароля наружу не откроется — это не настройка по
умолчанию, а отказ: `ListenAddress` возвращает ошибку, если попросить LAN без
пароля. На сервере проще всего пробросить порт:

```bash
ssh -N -L 8765:127.0.0.1:8765 <сервер>
```

и открыть `http://127.0.0.1:8765` у себя.

## Готовность

```bash
curl -fsS http://127.0.0.1:8765/healthz
```

- **200** — собирает. Остановленные наблюдения, если они есть, тут же перечислены.
- **503** — **все** включённые наблюдения остановлены: установка не собирает
  ничего.

Важное про 503: **перезапуск не поможет**. Программа не выходит при этом
специально — процесс, который вышел, systemd перезапустит в ту же стену
навсегда. Остановку снимает человек: кнопкой в панели или
`avitomon watch resume --all`. Поэтому в юните нет ничего, что дёргало бы
`/healthz`: это адрес для системы мониторинга, а не для супервизора.
