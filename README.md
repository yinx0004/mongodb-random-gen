# mgen

简单易用的 MongoDB 随机数据生成器：**配置写起来就像在描述数据本身**，并且能生成像真的一样的中英文姓名、手机号和地址。

```yaml
users:
  count: 1000
  fields:
    name: name                 # 张伟 / Emma Smith
    phone: phone               # 13812345678 / +1-415-555-0132
    address: address           # 浙江省杭州市西湖区文三路88号 / 123 Oak St, Austin, TX 78701
    age: 18..60
    level: [normal 80%, vip 20%]
    createdAt: 2023-01-01..now
```

## 安装

支持 MongoDB 4.0 及以上（已实测 4.0、4.4、7.0、8.0）。

**直接下载**：在 [Releases](https://github.com/yinx0004/mongodb-random-gen/releases/latest) 下载对应系统的压缩包（Linux / macOS / Windows，amd64 / arm64），解压即可使用，不需要安装 Go。

```bash
# 例：Linux amd64
curl -L https://github.com/yinx0004/mongodb-random-gen/releases/download/v0.1.0/mgen_0.1.0_linux_amd64.tar.gz | tar xz
./mgen_0.1.0_linux_amd64/mgen --version
```

macOS 用浏览器下载时，如果提示「无法验证开发者」，执行一次 `xattr -d com.apple.quarantine mgen` 即可。

**用 Go 安装**：

```bash
go install github.com/yinx0004/mongodb-random-gen/cmd/mgen@latest   # 需要 Go 1.25+，命令名为 mgen
```

或者从源码编译：

```bash
git clone https://github.com/yinx0004/mongodb-random-gen.git
cd mongodb-random-gen && go build -o mgen ./cmd/mgen
```

## 30 秒上手

```bash
mgen init                      # 生成带注释的示例配置 mgen.yaml
mgen preview mgen.yaml         # 打印几条看看（不连数据库）
mgen mgen.yaml                 # 写入 MongoDB（默认 localhost:27017，库名 test）
```

常用参数：

```bash
mgen mgen.yaml -n 100000                       # 覆盖条数
mgen mgen.yaml --uri mongodb://host:27017 --db shop --drop   # 指定库，写入前清空
mgen mgen.yaml --out ./data                    # 不写库，每个集合输出一个 <集合名>.jsonl（可用 mongoimport 导入）
mgen mgen.yaml --locale en                     # 临时切成英文数据
mgen types                                     # 查看全部类型和示例值
```

## 性能

默认 8 个 worker 并发生成、并发写入，每批 1000 条。本机 Docker 单实例实测（100 万条、11 个字段的用户文档）：

| | MongoDB 7.0 | MongoDB 4.0 |
|---|---|---|
| `-w 1`（串行） | 约 8.9 万条/秒 | 约 8.5 万条/秒 |
| 默认 `-w 8` | **约 30 万条/秒** | **约 31 万条/秒** |

- 服务器离得越远，并发的收益越大；可以用 `-w 16` 等试试，超过服务端承受能力后反而会变慢。
- **写入确认默认是 `w:1`**（主节点确认即返回）。MongoDB 5.0+ 副本集的默认值是 `majority`，要等多数节点复制，实测慢一倍左右；造测试数据一般不需要。需要时用 `--write-concern majority`；连接串里写了 `w=` 时，以连接串为准。
- 按 **Ctrl-C** 会停止发起新批次、等正在写的批次完成，然后报告准确的已写入条数；再按一次立即退出。

### 网络不稳定 / 超时

- **开始写入前，先把连接建好**：按 worker 数先建好连接，再开始写入，避免服务端忙着写入时还要处理新连接的握手和认证。
- **自动重试**：一批遇到网络错误、超时或主从切换时，最多重试 `--retries` 次（默认 5 次，间隔约 1、2、4、8、15 秒，总共约 30 秒，足够等副本集选主）。因为每条文档的 `_id` 都由 mgen 生成，重试不会产生重复数据，统计的条数也是准确的。
- **报错时指出可疑设置**：如果连接串里的 `connectTimeoutMS`、`socketTimeoutMS`、`timeoutMS` 和这次的错误对得上，会直接指出来。
- **开跑前提醒**：连接串里的 `connectTimeoutMS` 小于 10000 时，开始写入前会先给出警告（不影响运行）。

### 为什么写入过程中会新建连接？`connectTimeoutMS` 应该设多少？

驱动用的是连接池里的长连接，建好后会一直复用。但下面三种情况仍会新建连接：

1. **启动时，并发多少就要多少条连接。** 一条 MongoDB 连接同一时间只能处理一个请求，所以 8 个 worker 需要 8 条连接。连接池默认是空的，用到时才建，而且每次最多同时建 2 条。mgen 会在写入前先把这些连接建好。
2. **连接池被清空，所有连接都要重建。** 只要有一个请求遇到网络错误或超时（包括新连接握手超时），驱动就会认为这台服务器可能出了问题，关闭连接池里的**所有**连接。这是 MongoDB 驱动规范规定的行为。之后每个 worker 都要重新建连接；如果这时服务端正忙，新连接又可能超时，形成连锁反应。
3. **心跳超时（MongoDB 4.4 以下尤其要注意）。** 驱动每 10 秒通过一条单独的监控连接给服务端发一次心跳。服务端低于 4.4 时，**心跳的超时时间就是 `connectTimeoutMS`**。服务端忙于写入、心跳回复超时，驱动就会把服务器标记为不可用，并清空连接池，然后回到第 2 种情况。

另外，连接串里的 `maxIdleTimeMS`，以及中间会断开空闲连接的代理、负载均衡、防火墙，也会让连接被关闭后再重建。

**建议：** `connectTimeoutMS` 不要小于 10000，或者干脆不写，用默认的 30000。它限制的不只是 TCP 建连，而是“建连 + TLS + 握手 + 认证”的总时间，在 MongoDB 4.4 以下还同时决定心跳超时。设成 2000 这类很小的值，平时能用，但一遇到批量写入这样的高负载，就容易出现 `connection handshake ... context deadline exceeded` 这样的报错。


## 配置怎么写

整个文件只有两层：**集合**和**字段**。

```yaml
uri: mongodb://localhost:27017   # 可省略；也可用 --uri 或环境变量 MONGO_URI
db: shop                         # 可省略，默认 test
locale: zh                       # zh 中文（默认）| en 英文 | mixed 每条文档随机中/英

users:                           # 除了 uri / db / locale，顶层的每个键都是一个集合
  count: 1000                    # 条数，默认 1000
  drop: true                     # 可选：写入前清空
  locale: en                     # 可选：只对这个集合生效
  fields:                        # 字段写在这里
    ...
```

### 字段写法：值长成什么样，就生成什么样

| 写法 | 含义 | 示例结果 |
|---|---|---|
| `name` | 类型名（见下表，中英文名都行：`姓名`、`手机号`…） | `张伟` |
| `name_en` / `英文姓名` | 固定用英文（或 `_zh` / `中文…` 固定用中文） | `Emma Smith` |
| `18..60` | 整数范围 | `42` |
| `0..99.90` | 小数范围，小数位数照抄 | `37.25` |
| `2023-01-01..now` | 时间范围，也支持 `today`、`-30d`、`-6mo`、`-1y`、`+7d` | `2024-03-08T10:21:33Z` |
| `[a, b, c]` | 等概率选一个 | `b` |
| `[a 80%, b 15%, c]` | 按权重选，没写权重的平分剩下的比例 | `a` |
| `[app]` | 固定字符串 | `app` |
| `2` / `true` / `null` | 固定值 | `2` |
| 往下缩进写字段 | 嵌套文档 | `{ "city": "杭州市" }` |
| `text 10..50` | 一句话，10~50 个字 | `需要推进平台优化。` |

`_id` 不写会自动生成 ObjectId。

### 类型一览

| 分类 | 类型 |
|---|---|
| 人物 | `name` 姓名、`first_name` 名、`last_name` 姓、`gender` 性别、`username` 用户名、`email` 邮箱、`phone` 手机号、`id_card` 身份证 |
| 地址 | `address` 地址、`province` 省份/州、`city` 城市、`district` 区县、`street` 街道、`zipcode` 邮编、`country` 国家、`point` 坐标（GeoJSON） |
| 公司/网络 | `company` 公司、`job` 职位、`url`、`domain`、`ip`、`ipv6`、`mac`、`user_agent` |
| 文本 | `word` 词、`text [10..50]` 句子、`paragraph` 段落 |
| 其他 | `bool`、`objectid`、`uuid`、`date`（近一年）、`now`、`timestamp`、`timestamp_ms` |

运行 `mgen types` 可以看到每个类型的中英文示例值。

### 说明

- 中文数据使用真实的省/市/区名称和真实手机号段；身份证号校验位正确。这些都是随机生成的测试数据。
- `locale: mixed` 按**文档**随机选择语言：同一条文档里的姓名、手机号、地址保持同一种语言。
- 类型名区分大小写。想要和类型同名的固定字符串（比如 GeoJSON 的 `Point`），写成 `[Point]`。
- 写错时会提示行号和改法，例如：`第 4 行（users.phone）：未知类型 "phnoe"，是不是想写 phone？`
- 命令行语言跟随系统 `LANG`；可用 `MGEN_LANG=zh` 或 `MGEN_LANG=en` 强制指定。

更多示例见 [examples/](examples/)。

## 路线图

- [x] M1：基础写法、中英文数据、`run` / `preview` / `types` / `init` / `--out`；并发写入、写入确认级别；连接预热、自动重试、超时设置提示；预编译二进制（v0.1.0）
- [ ] M2：数组、缺失率/空值率、唯一值、跨集合引用（`ref`）、索引、随机种子
- [ ] M3：根据样例文档或线上集合自动推断配置（`mgen like` / `mgen init --like`）

设计细节见 [DESIGN.md](DESIGN.md)。

## License

[MIT](LICENSE)
