# mgen 设计稿（待评审）

> 一个开源、给团队用的 MongoDB 随机数据生成工具，Go 实现，单文件二进制。
> 核心目标：**不用看文档也能写出配置**。

## 1. 要解决的问题

造测试数据时，常见的痛点有两个：

1. **配置难写**：每个字段都要写成 `{"type": ..., "min": ..., ...}` 这样的结构，得先学会一套 schema 才能下手，
   看着配置也很难想象生成出来的数据长什么样。
2. **数据不像真的**：随机字符串能把库填满，但给不出“张伟 / 13812345678 / 浙江省杭州市西湖区…”这种看起来真实的数据，
   功能测试和演示时没法用。

mgen 的配置读起来要像在描述数据本身：

```yaml
users:
  count: 1000
  fields:
    name: 姓名
    age: 18..60
    status: [active, banned]
    createdAt: 2023-01-01..now
```

## 2. 设计原则

1. **值长成什么样，就生成什么样**：`18..60` 是整数，`9.9..99.9` 是小数，`2023-01-01..now` 是日期，`[a, b]` 是枚举。
2. **由浅入深**：最简单的写法只是 `字段: 类型`。只有确实要用的时候，才需要写更复杂的东西。
3. **写错立刻知道**：报错带行号，并给出改法提示（比如 “未知类型 `phnoe`，是不是想写 `phone`？”）。不会把写错的内容悄悄当成普通字符串插进去。
4. **零配置也能用**：给一条样例文档，或者指向一个线上集合，工具自动推断出配置。

## 3. 用户使用的三个层级

### 层级 1：零配置（照着造）

```bash
mgen like sample.json -n 1000 --db test --coll users     # 给一条样例 JSON，照着生成 1000 条
mgen like mongodb://host/shop.orders -n 10000000          # 照着线上集合的结构灌 1000 万条（压测）
```

推断规则会同时看字段名和值：字段名是 `name`、值是“张三”，推断为中文姓名；值像 `138…` 的 11 位数字，推断为中文手机号；数字推断为它附近的一个范围；ObjectId 保持 ObjectId，依此类推。

想微调时，先把推断结果导出成配置文件，改完再用：

```bash
mgen init --like sample.json > users.yaml
```

### 层级 2：简单配置（覆盖 80% 场景）

```yaml
uri: mongodb://localhost:27017      # 可省略，也可用环境变量 MONGO_URI 或 --uri
db: shop
locale: zh                          # zh | en | mixed，决定姓名/手机/地址用中文还是英文

users:
  count: 1000
  fields:
    name: name                      # 跟随 locale：zh -> 张伟，en -> Emma Smith
    phone: phone                    # zh -> 13812345678，en -> +1-415-555-0132
    email: email
    address: address                # zh -> 浙江省杭州市西湖区文三路 88 号
    age: 18..60                     # 整数范围
    balance: 0..10000.00            # 有小数点 = 小数，小数位数照抄（这里保留 2 位）
    gender: [男, 女]                 # 等概率选一个
    level: [normal 80%, vip 15%, svip 5%]   # 带权重
    vip: bool
    createdAt: 2023-01-01..now      # 日期范围，支持 now / -30d / -1y
    profile:                        # 嵌套文档：直接往下写
      city: city
      bio: text 10..50              # 中文/英文短文本，长度 10~50
```

**类型名同时支持中英文**：`姓名`=`name`、`手机号`=`phone`、`地址`=`address`、`邮箱`=`email`、`城市`=`city`，依此类推。
某个字段不想跟随全局 locale 时，可以写 `name_en` / `name_zh`，或者用层级 3 的写法。

**常量**：值写成只有一个元素的列表，比如 `source: [app]`。不另外引入新语法。

### 层级 3：进阶（按需使用）

任何字段都可以从一行写法展开成多行，用 `type` 加若干选项：

```yaml
orders:
  count: 100000
  indexes:
    - {orderNo: 1, unique: true}
  fields:
    orderNo: pattern SO-########          # # = 数字，? = 字母
    seqNo: seq 1000                        # 从 1000 开始自增
    userId: ref users                      # 引用 users 集合里的 _id（会自动先生成 users）
    email:
      type: email
      unique: true                         # 保证不重复
    phone:
      type: phone
      missing: 20%                         # 20% 的文档没有这个字段
    remark:
      type: text
      null: 70%                            # 70% 为 null
    customerName:
      type: name
      locale: en                           # 只有这个字段用英文
    tags:
      type: array
      of: [学生, 白领, 程序员]
      len: 0..3
      unique: true                         # 数组内元素不重复
    items:                                 # 子文档数组
      type: array
      len: 1..5
      of:
        sku: pattern SKU-#####
        qty: 1..5
        price: 9.9..999.0
```

“展开写法”的判定规则：一个 map 里有 `type` 键，`type` 的值是已知类型，其余键都是已知选项（len/of/unique/missing/null/locale/format/to …）。
不满足这些条件的 map，一律按嵌套文档处理。这样 GeoJSON 的 `type: Point` 也不会被误判。

## 4. 命令行

| 命令 | 作用 |
|---|---|
| `mgen run users.yaml` | 按配置写入 MongoDB（`run` 可省略：`mgen users.yaml`） |
| `mgen preview users.yaml` | 不连数据库，打印 3 条样例，用来检查配置 |
| `mgen like <sample.json \| mongodb://…/db.coll>` | 零配置，照着样例或线上集合生成 |
| `mgen init [--like …]` | 生成带注释的示例配置，或根据样例推断出配置 |
| `mgen types` | 列出全部类型，每个都带中英文示例值 |

通用参数：`-n` 条数、`--uri`、`--db`、`--drop` 先清空集合、`--seed` 固定随机种子（结果可复现）、`--out file.jsonl` 写文件不写库、`--workers` 并发数、`--batch` 每批条数。

## 5. 类型清单（第一版）

| 分类 | 类型 |
|---|---|
| 人 | name, first_name, last_name, gender, username, email, phone, id_card（仅 zh，校验位正确的假身份证号） |
| 地址 | address, province/state, city, district, street, zipcode, country, point（GeoJSON） |
| 公司/网络 | company, job, url, domain, ip, ipv6, mac, user_agent |
| 文本 | word, text 长度范围, paragraph, pattern |
| 数字/逻辑 | `a..b` 整数/小数范围, bool, seq, 枚举/权重枚举 |
| ID/时间 | objectid, uuid, `日期..日期`, now, timestamp（秒/毫秒） |
| 组合 | array, ref |

中文数据：常见姓氏 + 名字用字表、真实的省/市/区三级行政区划和常见街道名、真实号段的手机号。
英文数据：常见英文名和姓、美国州/城市/街道。词库编译进二进制，不需要联网。

## 6. 实现要点

- **语言/依赖**：Go 1.25+（golang.org/x 系列依赖的要求）；`mongo-driver/v2`（固定在 v2.3.x，以支持 MongoDB 4.0+）、`yaml.v3`、`cobra`。词库自己维护，不依赖 gofakeit，这样中英文数据的风格和质量能统一。
- **性能**：配置在启动时编译成只读的生成器树，可被多个 goroutine 共享。条数切成每 1 万条一个固定分区，分区 p 使用随机流 (seed, p)；默认 8 个 worker 从队列领取分区，各自生成、各自以每批 1000 条做无序 `InsertMany`。因此同一个 seed 生成的数据与 worker 数、批大小无关（M2 的 `--seed` 直接复用）。`seq` 计划用“分区起点 + 分区内偏移”计算，不需要跨 worker 加锁。实测本机单实例约 30 万条/秒。
- **ref**：先分析配置里集合之间的依赖，自动排出生成顺序，并且能检测循环引用。被引用的值优先从本次生成的数据里取（水塘抽样，最多保留 10 万个），没有的话再从库里 `$sample`。
- **unique**：在内存里去重，多次重试仍然冲突就报错，并提示“取值范围太小”。
- **报错**：yaml.v3 保留了行号，所有配置错误都能定位到“第 N 行、字段 x”，并给出最接近的正确写法。
- **目录结构**
  ```
  cmd/mgen/          CLI 入口
  internal/config/   YAML -> 字段规格（含简写解析、错误提示）
  internal/gen/      各类型生成器
  internal/data/     中英文词库
  internal/infer/    样例/集合 -> 配置
  internal/sink/     写 MongoDB / JSONL
  ```
- **发布**：goreleaser 出 mac/linux/windows 二进制，同时支持 `go install` 和 Homebrew tap。README 中英双语，开头放 30 秒上手示例。

## 7. 里程碑

| 阶段 | 内容 |
|---|---|
| M1 简单可用 | 层级 2 全部写法；中英文 name/phone/email/address；范围、枚举、日期、嵌套文档；`run` / `preview` / `types` / `init` / `--out`；并发写入、`--write-concern` |
| M2 进阶 | 层级 3：array、missing/null、unique、ref（自动排序）、indexes、seed |
| M3 零配置 | `like` / `init --like`（从样例或集合推断）、性能调优、goreleaser、文档 |

## 8. 已确认的决定

1. **展开写法**：用 `type` 键 + 已知选项来判定（第 3 节末尾的规则），不用 `$type`。
2. **命令行输出语言**：跟随系统 `LANG` / `LC_ALL` 自动切换（含 `zh` 时输出中文，否则英文），可用环境变量 `MGEN_LANG=zh|en` 强制指定。
3. **项目名**：`mgen`。
4. **默认 locale**：`zh`。`mixed` 按**文档**随机选择中文或英文，同一条文档内的姓名、手机号、地址保持同一种语言，不会出现中文名配美国电话。
5. Python 原型已删除。

## 9. 实现中补充的细节

- **相对时间单位**：`s` 秒、`min` 分、`h` 时、`d` 天、`w` 周、`mo` 月、`y` 年，如 `-30d..now`、`-6mo..now`。日期按 UTC 处理。
- **固定值**：数字、`true/false`、`null` 直接写就是固定值（`version: 2`）；字符串固定值写成单元素列表 `[app]`。
- **枚举权重**：可以只给部分选项写权重，剩下的平分余下比例，如 `[normal, vip 10%, svip 5%]`。
- **id_card** 始终生成中国身份证号，不受 locale 影响；英文环境下 `district` 返回 county。
- **`mgen init`**（生成带注释的示例配置）放进 M1，因为它直接解决“不知道怎么写”的问题；`init --like` 仍在 M3。
- **M1 里出现层级 3 写法**（`type:` 展开、`seq`、`pattern`、`ref`、`array`）时，给出“下个版本支持”的明确提示，不会静默生成错误数据。
- **Go module 路径**：`github.com/yinx0004/mongodb-random-gen`，命令名仍为 `mgen`（`go install …/cmd/mgen@latest`）。
- **类型名区分大小写**：避免把 GeoJSON 的 `type: Point` 之类的字面值误认为 `point` 类型。想要和类型同名的固定字符串，写成 `[Point]`；写错大小写时会提示正确的类型名。
- **`--out`**：给目录（或不带扩展名的路径）时，每个集合输出一个 `<集合名>.jsonl`；给 `.jsonl` 文件名时只允许单个集合；`-` 输出到屏幕。完成后打印对应的 mongoimport 命令。
- **支持的 MongoDB 版本：4.0 及以上**。mongo-driver v2.4 起不再支持 4.0、v2.10 起要求 4.4，因此驱动固定在 v2.3.x（覆盖 4.0 ~ 8.0）。连到更老的服务端时，会明确提示“版本太旧”。已实测 4.0 / 4.4 / 7.0 / 8.0.4。
- **并发写入提前到 M1 完成**（原计划在 M2）：用户反馈比 tslab 慢很多，原因是串行写入（每批都要等一次网络往返）和副本集默认 `majority` 写入确认。现在默认 `-w 8`，写入确认默认 `w:1`（`--write-concern` 可改；连接串里的 `w=` 优先）。
- **Ctrl-C**：停止发起新批次，正在写的批次不取消、等它完成，保证报告的已写入条数与库里一致；第二次 Ctrl-C 立即退出（退出码 130）。
- **连接预热**：用户在带 `connectTimeoutMS=2000` 的连接串下遇到握手超时。原因是 worker 在写入高峰期才去新建连接，服务端繁忙时，握手加认证超过了 2 秒。现在连接时把 `minPoolSize` 设为 worker 数，并通过连接池事件等待主节点上的连接全部就绪（最多 30 秒；遇到连接失败就不再等，交给重试处理），再开始写入。
- **批次重试**：网络错误、超时、带 `RetryableWriteError` 标签的错误，以及选主期间的 server selection 错误，都会重试。默认 5 次，退避约 1/2/4/8/15 秒，带 ±20% 随机抖动。重试是幂等的：`_id` 由客户端生成，上一次其实已经写入的文档，在重试时会报 `_id` 重复，这些不计为失败。最终仍失败时返回 `RetryError`，报错里注明“这一批已重试 N 次”。
- **超时提示按错误类型对应**：握手或建连阶段超时 → `connectTimeoutMS`；读取响应超时 → `socketTimeoutMS`；任何超时都可能和 `timeoutMS` 有关；连接被对端关闭 → 网络、代理或防火墙。这几种都不是并发参数造成的，所以连接阶段的报错不再建议降低并发。
- **故障注入验证**：用一个 TCP 代理复现了“负载下握手变慢”（旧版本报出与用户相同的错误，新版本正常完成）和“频繁断连”（重试后条数精确）。
- **开跑前的 connectTimeoutMS 警告**：连接后用 `buildInfo` 取服务端版本。连接串里 `connectTimeoutMS` 小于 10000 时，先在 stderr 打一条警告，不中断运行；服务端低于 4.4 时，额外说明心跳超时也等于它（驱动源码 `getHeartbeatTimeout`：不支持流式心跳的服务端，心跳超时 = connectTimeoutMS）。README 里解释了长连接为什么还会新建（并发需要多条连接、连接池被清空、心跳超时）。

- **v0.1.0 发布**：先手工交叉编译 linux / darwin / windows × amd64 / arm64 共 6 个包（`CGO_ENABLED=0 -trimpath -ldflags "-s -w"`，每个包含 mgen、LICENSE、README），附 `checksums.txt`（sha256），上传到 GitHub Release。goreleaser、Homebrew tap 仍留在 M3。
- **在 macOS 上手工打包的注意事项**：macOS 自带的 tar（bsdtar）默认会把扩展属性（如 `com.apple.provenance`）打进包里，一部分变成 `._xxx` 文件（AppleDouble），一部分变成 pax 扩展头。Linux 上用 GNU tar 解压时会报 `Ignoring unknown extended header keyword`，还会多出 `._` 文件（v0.1.0 第一次上传时就出现了，已替换）。手工发版的步骤：

  ```bash
  V=0.1.0   # 和 cmd/mgen/main.go 里的 version 保持一致
  for t in linux/amd64 linux/arm64 darwin/amd64 darwin/arm64 windows/amd64 windows/arm64; do
    os=${t%/*}; arch=${t#*/}; name=mgen_${V}_${os}_${arch}; ext=; [ $os = windows ] && ext=.exe
    mkdir -p dist/$name
    CGO_ENABLED=0 GOOS=$os GOARCH=$arch go build -trimpath -ldflags "-s -w -X main.version=$V" -o dist/$name/mgen$ext ./cmd/mgen
    cp LICENSE README.md dist/$name/
    xattr -rc dist/$name
    if [ $os = windows ]; then
      (cd dist && zip -qrX $name.zip $name)
    else
      (cd dist && COPYFILE_DISABLE=1 tar --format ustar --no-mac-metadata --no-xattrs \
          --uid 0 --gid 0 --uname root --gname root -czf $name.tar.gz $name)
    fi
    rm -rf dist/$name
  done
  (cd dist && shasum -a 256 mgen_* > checksums.txt)
  ```

  - `COPYFILE_DISABLE=1`：不生成 `._` 文件；`--no-mac-metadata --no-xattrs`：不写扩展属性；`--format ustar`：用最普通的 tar 格式，没有 pax 扩展头；属主统一为 root。zip 用 `-X` 不存额外属性，也不会生成 `__MACOSX` 目录（那是 Finder「压缩」才会加的）。
  - 发布前检查：`tar tzf` 里不能有 `._` 开头的条目；在 Linux 容器里用 GNU tar 解压不能有警告，并运行 `mgen --version`；`shasum -a 256 -c checksums.txt` 全部 OK。
  - 发布：先打带注释的 tag 并推送，再 `gh release create vX.Y.Z --verify-tag --notes-file <说明> dist/*`；替换已上传的文件用 `gh release upload vX.Y.Z --clobber`。
