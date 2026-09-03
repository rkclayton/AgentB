[CmdletBinding()]
param(
    [string]$BaseUrl = 'http://127.0.0.1:8080',
    [string]$PriorStack = 'ollama',
    [string]$PriorStackContext = 'unknown'
)

$ErrorActionPreference = 'Stop'
$resultsDirectory = Join-Path $PSScriptRoot 'results'
$samplesDirectory = Join-Path $PSScriptRoot 'samples'
New-Item -ItemType Directory -Force -Path $resultsDirectory, $samplesDirectory | Out-Null

function ConvertTo-CompactJson {
    param([Parameter(Mandatory=$true)]$Value)
    return ($Value | ConvertTo-Json -Depth 30 -Compress)
}

function Invoke-Endpoint {
    param(
        [Parameter(Mandatory=$true)][string]$Path,
        [ValidateSet('GET','POST')][string]$Method = 'GET',
        $Body = $null,
        [int]$TimeoutSeconds = 120
    )

    $uri = if ($Path -match '^https?://') { $Path } else { "$BaseUrl$Path" }
    try {
        $parameters = @{
            Uri = $uri
            Method = $Method
            UseBasicParsing = $true
            TimeoutSec = $TimeoutSeconds
        }
        if ($null -ne $Body) {
            $parameters.ContentType = 'application/json'
            $parameters.Body = ConvertTo-CompactJson $Body
        }
        $response = Invoke-WebRequest @parameters
        $parsed = $null
        if ($response.Content) {
            try { $parsed = $response.Content | ConvertFrom-Json } catch {}
        }
        return [pscustomobject]@{
            status = [int]$response.StatusCode
            raw = [string]$response.Content
            json = $parsed
            error = $null
        }
    } catch {
        $status = 0
        $raw = ''
        if ($_.Exception.Response) {
            try { $status = [int]$_.Exception.Response.StatusCode } catch {}
            try {
                $stream = $_.Exception.Response.GetResponseStream()
                $reader = New-Object IO.StreamReader($stream)
                $raw = $reader.ReadToEnd()
            } catch {}
        }
        if (-not $raw -and $_.ErrorDetails.Message) { $raw = $_.ErrorDetails.Message }
        $parsed = $null
        if ($raw) { try { $parsed = $raw | ConvertFrom-Json } catch {} }
        return [pscustomobject]@{
            status = $status
            raw = $raw
            json = $parsed
            error = $_.Exception.Message
        }
    }
}

function Get-TokenCount {
    param([Parameter(Mandatory=$true)][AllowEmptyString()][string]$Content)
    $response = Invoke-Endpoint -Path '/tokenize' -Method POST -Body @{
        content = $Content
        add_special = $false
    }
    if ($response.status -ne 200 -or $null -eq $response.json.tokens) {
        throw "tokenize failed: HTTP $($response.status) $($response.raw)"
    }
    return [int]$response.json.tokens.Count
}

function New-PaddedContent {
    param([Parameter(Mandatory=$true)][int]$TargetTokens, [string]$Prefix = '')
    $repeat = [Math]::Max(1, $TargetTokens)
    $content = $Prefix + (' token' * $repeat)
    for ($attempt = 0; $attempt -lt 4; $attempt++) {
        $count = Get-TokenCount $content
        if ([Math]::Abs($count - $TargetTokens) -le [Math]::Max(8, [int]($TargetTokens * 0.01))) {
            break
        }
        $repeat = [Math]::Max(1, [int][Math]::Round($repeat * $TargetTokens / [Math]::Max(1, $count)))
        $content = $Prefix + (' token' * $repeat)
    }
    return [pscustomobject]@{ content = $content; tokens = (Get-TokenCount $content) }
}

function Get-Median {
    param([Parameter(Mandatory=$true)][double[]]$Values)
    $sorted = @($Values | Sort-Object)
    if ($sorted.Count -eq 0) { return 0 }
    if ($sorted.Count % 2) { return [double]$sorted[[int]($sorted.Count / 2)] }
    $right = [int]($sorted.Count / 2)
    return ([double]$sorted[$right - 1] + [double]$sorted[$right]) / 2
}

function Measure-EndpointMedian {
    param([string]$Path, $Body, [int]$Count = 20)
    $measurements = @()
    for ($index = 0; $index -lt $Count; $index++) {
        $watch = [Diagnostics.Stopwatch]::StartNew()
        $response = Invoke-Endpoint -Path $Path -Method POST -Body $Body -TimeoutSeconds 120
        $watch.Stop()
        if ($response.status -ne 200) { throw "$Path timing call failed: HTTP $($response.status) $($response.raw)" }
        $measurements += $watch.Elapsed.TotalMilliseconds
    }
    return [Math]::Round((Get-Median $measurements), 0)
}

function New-ChatBody {
    param($Messages, [int]$MaxTokens = 128, $Tools = $null, $ToolChoice = $null, $TemplateKwargs = $null)
    $body = [ordered]@{
        model = 'qwen3.8-27b'
        messages = $Messages
        max_tokens = $MaxTokens
        temperature = 0.3
        stream = $false
    }
    if ($null -ne $Tools) { $body.tools = $Tools }
    if ($null -ne $ToolChoice) { $body.tool_choice = $ToolChoice }
    if ($null -ne $TemplateKwargs) { $body.chat_template_kwargs = $TemplateKwargs }
    return $body
}

$report = [ordered]@{
    started_at = [DateTime]::UtcNow.ToString('o')
    base_url = $BaseUrl
    canaries = [ordered]@{}
}

Write-Host '[1/11] health and props'
$health = Invoke-Endpoint -Path '/health'
$props = Invoke-Endpoint -Path '/props'
$report.canaries.health_props = [ordered]@{
    health_status = $health.status
    health = $health.json
    props_status = $props.status
    props = $props.json
}
if ($health.status -ne 200 -or $props.status -ne 200) { throw 'health/props canary failed' }
$nContext = [int]$props.json.default_generation_settings.n_ctx

Write-Host '[2/11] overflow behavior'
$overflowInput = New-PaddedContent -TargetTokens ($nContext + 2000) -Prefix 'Overflow canary. '
$overflow = Invoke-Endpoint -Path '/v1/chat/completions' -Method POST -TimeoutSeconds 300 -Body (New-ChatBody -Messages @(
    @{ role = 'user'; content = $overflowInput.content }
) -MaxTokens 8)
$reportedPromptTokens = $null
if ($overflow.json.usage) { $reportedPromptTokens = $overflow.json.usage.prompt_tokens }
$overflowBehavior = if ($overflow.status -ge 400) { 'error' } elseif ($reportedPromptTokens -and $reportedPromptTokens -lt $overflowInput.tokens) { 'truncates' } else { 'accepted' }
$report.canaries.overflow = [ordered]@{
    sent_tokens = $overflowInput.tokens
    status = $overflow.status
    behavior = $overflowBehavior
    reported_prompt_tokens = $reportedPromptTokens
    response = $overflow.raw
}

Write-Host '[3/11] tokenizer'
$fixedSample = '0123456789' * 100
$report.canaries.tokenizer = [ordered]@{
    sample_characters = $fixedSample.Length
    token_count = Get-TokenCount $fixedSample
}

Write-Host '[4/11] tool calling'
$readFileTool = @{
    type = 'function'
    function = @{
        name = 'read_file'
        description = 'Read a UTF-8 text file in the workspace.'
        parameters = @{
            type = 'object'
            properties = @{
                path = @{ type = 'string' }
                offset = @{ type = 'integer' }
                limit = @{ type = 'integer' }
            }
            required = @('path')
        }
    }
}
$toolPrompt = @{ role = 'user'; content = 'Read main.go' }
$toolFirst = Invoke-Endpoint -Path '/v1/chat/completions' -Method POST -TimeoutSeconds 300 -Body (New-ChatBody -Messages @($toolPrompt) -MaxTokens 512 -Tools @($readFileTool) -ToolChoice 'required')
$assistantToolMessage = $toolFirst.json.choices[0].message
$toolCall = $assistantToolMessage.tool_calls[0]
$argumentsParsed = $false
try {
    $arguments = $toolCall.function.arguments | ConvertFrom-Json
    $argumentsParsed = ($null -ne $arguments.path)
} catch {}
$toolSecond = $null
if ($toolCall.id) {
    $toolSecond = Invoke-Endpoint -Path '/v1/chat/completions' -Method POST -TimeoutSeconds 300 -Body (New-ChatBody -Messages @(
        $toolPrompt,
        $assistantToolMessage,
        @{ role = 'tool'; tool_call_id = $toolCall.id; content = "package main`n`nfunc main() {}`n" }
    ) -MaxTokens 256 -Tools @($readFileTool) -ToolChoice 'auto')
}
$slots = Invoke-Endpoint -Path '/slots'
$chatFormat = $null
if ($slots.json -and $slots.json.Count -gt 0) { $chatFormat = $slots.json[0].params.chat_format }
$report.canaries.tool_calling = [ordered]@{
    first_status = $toolFirst.status
    function_name = $toolCall.function.name
    arguments = $toolCall.function.arguments
    arguments_parsed = $argumentsParsed
    second_status = if ($toolSecond) { $toolSecond.status } else { 0 }
    second_finish_reason = if ($toolSecond) { $toolSecond.json.choices[0].finish_reason } else { $null }
    second_content = if ($toolSecond) { $toolSecond.json.choices[0].message.content } else { $null }
    chat_format = $chatFormat
}

Write-Host '[5/11] reasoning control'
$reasoningCases = [ordered]@{
    low = @{ reasoning_effort = 'low' }
    medium = @{ reasoning_effort = 'medium' }
    xhigh = @{ reasoning_effort = 'xhigh' }
    disabled = @{ enable_thinking = $false }
}
$reasoningResults = [ordered]@{}
foreach ($caseName in $reasoningCases.Keys) {
    $response = Invoke-Endpoint -Path '/v1/chat/completions' -Method POST -TimeoutSeconds 300 -Body (New-ChatBody -Messages @($toolPrompt) -MaxTokens 2048 -Tools @($readFileTool) -ToolChoice 'required' -TemplateKwargs $reasoningCases[$caseName])
    $reasoning = [string]$response.json.choices[0].message.reasoning_content
    $reasoningTokenCount = if ($reasoning) { Get-TokenCount $reasoning } else { 0 }
    $reasoningResults[$caseName] = [ordered]@{
        status = $response.status
        reasoning_characters = $reasoning.Length
        reasoning_tokens = $reasoningTokenCount
        function_name = $response.json.choices[0].message.tool_calls[0].function.name
        error = $response.error
        raw = if ($response.status -ne 200) { $response.raw } else { $null }
    }
}
$report.canaries.reasoning = $reasoningResults

Write-Host '[6/11] prefix cache reuse'
$systemPadding = New-PaddedContent -TargetTokens 200 -Prefix 'You are a precise assistant. '
$userPadding = New-PaddedContent -TargetTokens 3000 -Prefix 'Summarize this repeated context in one short sentence. '
$cacheMessages = @(
    @{ role = 'system'; content = $systemPadding.content },
    @{ role = 'user'; content = $userPadding.content }
)
$cacheResults = @()
for ($turn = 1; $turn -le 3; $turn++) {
    $cacheResponse = Invoke-Endpoint -Path '/v1/chat/completions' -Method POST -TimeoutSeconds 300 -Body (New-ChatBody -Messages $cacheMessages -MaxTokens 64 -TemplateKwargs @{ reasoning_effort = 'low' })
    $usage = $cacheResponse.json.usage
    $timings = $cacheResponse.json.timings
    $cachedTokens = 0
    if ($usage.prompt_tokens_details.cached_tokens) { $cachedTokens = [int]$usage.prompt_tokens_details.cached_tokens }
    $cacheResults += [ordered]@{
        turn = $turn
        status = $cacheResponse.status
        prompt_tokens = $usage.prompt_tokens
        cached_tokens = $cachedTokens
        cache_n = $timings.cache_n
        prompt_ms = $timings.prompt_ms
    }
    if ($turn -lt 3 -and $cacheResponse.status -eq 200) {
        $cacheMessages += $cacheResponse.json.choices[0].message
        $cacheMessages += @{ role = 'user'; content = 'continue' }
    }
}
$lastCache = $cacheResults[-1]
$cacheRatio = if ($lastCache.prompt_tokens) { [Math]::Round([double]$lastCache.cached_tokens / [double]$lastCache.prompt_tokens, 4) } else { 0 }
$report.canaries.cache_reuse = [ordered]@{ requests = $cacheResults; final_ratio = $cacheRatio }

Write-Host '[7/11] streaming shape'
$streamBody = New-ChatBody -Messages @($toolPrompt) -MaxTokens 512 -Tools @($readFileTool) -ToolChoice 'required'
$streamBody.stream = $true
$streamBody.stream_options = @{ include_usage = $true }
$streamBody.return_progress = $true
$streamBody.timings_per_token = $false
$streamRequestPath = Join-Path $resultsDirectory 'stream-request.json'
$streamSamplePath = Join-Path $samplesDirectory 'stream-toolcall.txt'
[IO.File]::WriteAllText($streamRequestPath, (ConvertTo-CompactJson $streamBody), (New-Object Text.UTF8Encoding($false)))
$curlOutput = & curl.exe -sS -N -H 'Content-Type: application/json' --data-binary "@$streamRequestPath" "$BaseUrl/v1/chat/completions" 2>&1
[IO.File]::WriteAllLines($streamSamplePath, [string[]]$curlOutput, (New-Object Text.UTF8Encoding($false)))
$streamJson = @()
foreach ($line in $curlOutput) {
    if ($line -like 'data: *' -and $line -ne 'data: [DONE]') {
        try { $streamJson += ($line.Substring(6) | ConvertFrom-Json) } catch {}
    }
}
$finalUsageChunk = $streamJson | Where-Object { $_.usage } | Select-Object -Last 1
$argumentDeltas = @($streamJson | ForEach-Object { $_.choices[0].delta.tool_calls } | Where-Object { $_ } | ForEach-Object { $_[0].function.arguments } | Where-Object { $_ })
$report.canaries.streaming = [ordered]@{
    transcript = $streamSamplePath
    chunk_count = $streamJson.Count
    final_has_usage = ($null -ne $finalUsageChunk.usage)
    final_has_timings = ($null -ne $finalUsageChunk.timings)
    return_progress = [bool]($streamJson | Where-Object { $_.prompt_progress } | Select-Object -First 1)
    tool_call_deltas = if ($argumentDeltas.Count -gt 1) { 'incremental' } else { 'final' }
    argument_delta_count = $argumentDeltas.Count
}

Write-Host '[8/11] speed'
$speedInput = New-PaddedContent -TargetTokens 4000 -Prefix 'After reading the padding, count from 1 to 80, one number per line. '
$speedResponse = Invoke-Endpoint -Path '/v1/chat/completions' -Method POST -TimeoutSeconds 600 -Body (New-ChatBody -Messages @(
    @{ role = 'user'; content = $speedInput.content }
) -MaxTokens 1024 -TemplateKwargs @{ reasoning_effort = 'medium' })
$report.canaries.speed = [ordered]@{
    status = $speedResponse.status
    input_tokens = $speedInput.tokens
    prompt_per_second = $speedResponse.json.timings.prompt_per_second
    predicted_per_second = $speedResponse.json.timings.predicted_per_second
    prompt_ms = $speedResponse.json.timings.prompt_ms
    predicted_ms = $speedResponse.json.timings.predicted_ms
    predicted_n = $speedResponse.json.timings.predicted_n
}

Write-Host '[9/11] sampling defaults'
$generationSettings = $props.json.default_generation_settings.params
$report.canaries.sampling = [ordered]@{
    temperature = $generationSettings.temperature
    top_k = $generationSettings.top_k
    top_p = $generationSettings.top_p
    min_p = $generationSettings.min_p
    repeat_penalty = $generationSettings.repeat_penalty
    mirostat = $generationSettings.mirostat
}

Write-Host '[10/11] prior stack'
$report.canaries.prior_stack = [ordered]@{
    name = $PriorStack
    context = $PriorStackContext
}

Write-Host '[11/11] tokenize/apply-template slot blocking'
$tokenizeTimingBody = @{ content = ('slot timing sample ' * 30); add_special = $false }
$templateTimingBody = @{
    messages = @(
        @{ role = 'system'; content = 'Be concise.' },
        @{ role = 'user'; content = 'Say hello.' },
        @{ role = 'assistant'; content = 'Hello.' },
        @{ role = 'user'; content = 'Say goodbye.' }
    )
    add_generation_prompt = $true
}
$tokenizeIdle = Measure-EndpointMedian -Path '/tokenize' -Body $tokenizeTimingBody
$templateIdle = Measure-EndpointMedian -Path '/apply-template' -Body $templateTimingBody

$busyBody = New-ChatBody -Messages @(
    @{ role = 'user'; content = 'Count from 1 to 400, one number per line. Do not skip any numbers.' }
) -MaxTokens 4096 -TemplateKwargs @{ reasoning_effort = 'medium' }
$busyJson = ConvertTo-CompactJson $busyBody
$generationJob = Start-Job -ScriptBlock {
    param($Uri, $Json)
    Invoke-WebRequest -UseBasicParsing -Uri "$Uri/v1/chat/completions" -Method Post -ContentType 'application/json' -Body $Json -TimeoutSec 600 | Out-Null
} -ArgumentList $BaseUrl, $busyJson
Start-Sleep -Seconds 2
$tokenizeBusy = Measure-EndpointMedian -Path '/tokenize' -Body $tokenizeTimingBody
$templateBusy = Measure-EndpointMedian -Path '/apply-template' -Body $templateTimingBody
if ($generationJob.State -eq 'Running') { Stop-Job $generationJob }
Remove-Job $generationJob -Force

$tokenizeRatio = if ($tokenizeIdle) { [Math]::Round($tokenizeBusy / $tokenizeIdle, 2) } else { 0 }
$templateRatio = if ($templateIdle) { [Math]::Round($templateBusy / $templateIdle, 2) } else { 0 }
$maxRatio = [Math]::Max($tokenizeRatio, $templateRatio)
$blockState = if ($maxRatio -ge 3) { 'yes' } elseif ($maxRatio -ge 1.5) { 'partial' } else { 'no' }
$report.canaries.slot_blocking = [ordered]@{
    tokenize_idle_ms = [int]$tokenizeIdle
    tokenize_busy_ms = [int]$tokenizeBusy
    tokenize_ratio = $tokenizeRatio
    apply_template_idle_ms = [int]$templateIdle
    apply_template_busy_ms = [int]$templateBusy
    apply_template_ratio = $templateRatio
    blocks_on_slot = $blockState
}

$report.finished_at = [DateTime]::UtcNow.ToString('o')
$reportPath = Join-Path $resultsDirectory 'latest.json'
[IO.File]::WriteAllText($reportPath, ($report | ConvertTo-Json -Depth 30), (New-Object Text.UTF8Encoding($false)))
Write-Host "Probe report written to $reportPath"
$report | ConvertTo-Json -Depth 8
