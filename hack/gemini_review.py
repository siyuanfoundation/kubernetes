import google.generativeai as genai
import os
import os.path
from github import Github
from google.cloud import storage
import random
import re
import requests
import json

# Set the maximum number of comments to post on the PR
MAX_COMMENTS = 20

total_comments_posted = 0



def should_skip_file(filename):
    if not filename:
        return True
    if not filename.endswith(".go") and not filename.endswith(".py"):
        return True
    if filename.endswith("_test.go"):
        return True
    if filename.endswith("_test.py"):
        return True
    if "/test/" in filename:
        return True
    if "_generated" in filename:
        return True
    return False

def should_keep_comment(comment):
    # return comment['commit_id'] == comment['original_commit_id']:
    return comment['commenter'] in ['liggit', 'thockin']
    # return False

def search_pull_requests_with_label(repo, label, github_token, pr_limit=20):
    url = f"https://api.github.com/search/issues"
    query = f"repo:{repo} is:pr label:{label}"
    page_size = 100 if pr_limit > 100  else pr_limit
    params = {
        "q": query,
        "sort": "updated",
        "order": "desc",
        "per_page": page_size,
        "page": 1
    }
    headers = {
        "Authorization": f"Bearer {github_token}",
        "Accept": "application/vnd.github.v3+json"
    }
    
    all_prs = []

    while True:
        response = requests.get(url, headers=headers, params=params)
        response.raise_for_status()
        data = response.json()
        all_prs.extend(data.get('items', []))
        if len(all_prs) >= pr_limit:
            break
        if "next" in response.links:
            params["page"] += 1
        else:
            break
    print(f"Total number of pull requests with label {label}: ", len(all_prs))
    return all_prs

def get_pr_details(repo_name, pr_number, github_token):
    g = Github(github_token)
    repo = g.get_repo(repo_name)
    pr = repo.get_pull(pr_number)
    result = {'title': pr.title, 'number': pr.number, 'url': pr.url, 'body': pr.body, 'author': pr.user.login, 'commits': []}
    try:
        commits = list(pr.get_commits())
        for commit in commits:
            commit_data = {'commit_id': commit.sha, 'commit_url': commit.html_url}
            commit_data['files'] = get_commit_diff_files(commit)
            result['commits'].append(commit_data)
    except Exception as e:
        print(f"Error getting commits: {e}")
        return None
    # print(json.dumps(result, indent=2))
    return result
    
def get_review_comments(repo, pr_number, github_token):
    url = f"https://api.github.com/repos/{repo}/pulls/{pr_number}/comments"
    comments_by_commit = {}
    page = 1
    headers = {
        "Authorization": f"Bearer {github_token}",
        "Accept": "application/vnd.github.v3+json"
    }
    comment_keys_to_keep = [
        'url', 'created_at', 'updated_at', 'diff_hunk', 'body', 'commit_id', 'original_commit_id', 
        'path', 'line', 'start_line', 'side', 'start_side', 'original_line', 'original_start_line']

    while True:
        response = requests.get(url, headers=headers, params={"per_page": 100, "page": page})
        response.raise_for_status()
        data = response.json()
        for comment_data in data:
            comment = {}
            if should_skip_file(comment_data['path']):
                continue
            # only keep the first comment in a conversation
            if 'in_reply_to_id' in comment_data:
                continue
            for key in comment_keys_to_keep:
                if key in comment_data:
                    comment[key] = comment_data[key]
            if 'user' in comment_data:
                comment['commenter'] = comment_data['user']['login']
            if 'commit_id' not in comment_data:
                continue
            if not should_keep_comment(comment):
                continue
            commit_id = comment_data['commit_id']
            if commit_id not in comments_by_commit:
                comments_by_commit[commit_id] = []
            comments_by_commit[commit_id].append(comment)

        if len(data) < 100:
            break
        page += 1
    commit_comments = []
    for key, value in comments_by_commit.items():
        if len(value) == 0:
            continue
        files = get_commit_details(repo, key, github_token)
        if len(files) == 0:
            continue
        diff_file_list = [file['filename'] for file in files]
        comments = list(filter(lambda x: not x['path'] or x['path'] in diff_file_list, value))
        if len(comments) == 0:
            continue
        comments.sort(key=lambda x: x['updated_at'])
        commit_comments.append({"commit_id": key, "comments": comments, "comment_count": len(comments), "files": files,
                                "commit_url": f"https://api.github.com/repos/{repo}/commits/{key}"})
    return commit_comments

def get_pull_request_comments(prs, repo, github_token):
    all_prs = []
    pr_keys_to_keep = ['title', 'number', 'url', 'body', 'created_at', 'updated_at']
    count = 0
    for pr in prs:
        pr_trimmed = {}
        count = count + 1
        print(f"{count}/{len(prs)}: {pr['url']}")
        for key in pr_keys_to_keep:
            if key in pr:
                pr_trimmed[key] = pr[key]
            if 'user' in pr:
                pr_trimmed['author'] = pr['user']['login']
        comments_by_commit = get_review_comments(repo, pr['number'], github_token)
        if len(comments_by_commit) == 0:
            continue
        pr_trimmed['commits'] = comments_by_commit
        all_prs.append(pr_trimmed)
    return all_prs

def crawl_pr_comments_to_gcs(repo_name, label, github_token, bucket_name, destination_blob_name, pr_limit=20):
    local_output_file='pr_comments.json'
    prs = search_pull_requests_with_label(repo_name, label, github_token, pr_limit=pr_limit)
    prs = get_pull_request_comments(prs, repo_name, github_token)
    print("number of final saved prs: ", len(prs))
    with open(local_output_file, 'w') as file:
        json.dump(prs, file, indent=2)
    upload_file_to_gcs(bucket_name, local_output_file, destination_blob_name)
    return prs

def get_commit_details(repo_name, commit_id, github_token):
    g = Github(github_token)
    repo = g.get_repo(repo_name)
    try:
        commit = repo.get_commit(commit_id)
    except Exception as e:
        print(f"Error getting commit details: {e}")
        return []
    return get_commit_diff_files(commit)

def get_commit_diff_files(commit):
    diff_files = []
    for file in commit.files:
        if not should_skip_file(file.filename):
            if file.patch:
                diff_files.append({
                    'filename': file.filename,
                    'patch': file.patch
                })
    return diff_files
    

def get_pr_latest_commit_diff_files(repo_name, pr_number, github_token):
    """Retrieves diff information for each file in the latest commit of a PR, excluding test files and generated files."""
    g = Github(github_token)
    repo = g.get_repo(repo_name)
    pr = repo.get_pull(pr_number)

    try:
        commits = list(pr.get_commits())
        if commits:
            latest_commit = commits[-1]
            files = latest_commit.files
            diff_files = []
            for file in files:
                if not should_skip_file(file.filename):
                    if file.patch:
                        diff_files.append(file)
            return diff_files
        else:
            return None
    except Exception as e:
        print(f"Error getting diff files from latest commit: {e}")
        return None

def download_and_combine_guidelines(bucket_name, prefix):
    """Downloads markdown files from GCS using the google-cloud-storage library."""
    try:
        storage_client = storage.Client()
        bucket = storage_client.bucket(bucket_name)
        blobs = bucket.list_blobs(prefix=prefix)  # Use prefix for efficiency

        guidelines_content = ''
        for blob in blobs:
            if blob.name.endswith(".md"):
                guidelines_content += blob.download_as_text() + "\n\n"
        return guidelines_content

    except Exception as e:
        print(f"Error downloading or combining guidelines: {e}")

def upload_file_to_gcs(bucket_name, source_file_name, destination_blob_name):
    """Uploads a file to the bucket."""
    try:
        storage_client = storage.Client()
        bucket = storage_client.bucket(bucket_name)
        blob = bucket.blob(destination_blob_name)
        blob.upload_from_filename(source_file_name)
        print(
            f"File {source_file_name} uploaded to {destination_blob_name}."
        )
    except Exception as e:
        print(f"Error uploading file {source_file_name} to GCS: {e}")

def download_file_as_json(bucket_name, source_blob_name):
    print("trying to download GCS: gs://{bucket_name}/{source_blob_name} as json")
    try:
        storage_client = storage.Client()
        bucket = storage_client.bucket(bucket_name)
        blob = bucket.blob(source_blob_name)
        if not blob.exists():
            return None
        data = json.loads(blob.download_as_string())
        print(f"Successfully downloaded json from GCS {source_blob_name}")
        return data
    except Exception as e:
        print(f"Error downloading file {source_blob_name} from GCS: {e}")
        return None

def download_and_combine_pr_comments(bucket_name, prefix):
    """Downloads text files from GCS using the google-cloud-storage library."""
    try:
        storage_client = storage.Client()
        bucket = storage_client.bucket(bucket_name)
        blobs = bucket.list_blobs(prefix=prefix)  # Use prefix for efficiency
        pr_comments_content = ""
        # TODO: Skip for now, since it is too large
        # for blob in blobs:
        #     if blob.name.endswith(".txt"):
        #         pr_comments_content += blob.download_as_text() + "\n\n"
        return pr_comments_content
    except Exception as e:
        print(f"Error downloading or combining PR comments: {e}")
        return ""
    
def commit_prompt_template(pr, commit_index, is_example=False):
    commit = pr['commits'][commit_index]
    files_json = json.dumps(commit['files'])
    prompt = f"""

Review the following commit from pull request #{pr['number']}:
Pull Request Title: {pr['title']}
Pull Reuqest Body: {pr['body']}
File changes in json format: 
{files_json}
"""
    if is_example:
        for comment in commit['comments']:
            prompt += f"""
path: 
{comment['path']}
diff: 
{comment['diff_hunk']}
Provide your review comments in the following format:
```
line <line_number>: <comment>
line <line_number>: <comment>
...and so on
```

```
line {comment['original_line']}: {comment['body']}
```
"""

    return prompt

def generate_gemini_review_with_annotations_multi_turn(pr, api_key, guidelines, pr_comments, max_examples=5):
    """Generates a code review with annotations using Gemini."""
    commits = pr['commits']
    if not commits:
        return None
    commit = commits[-1]
    diff_files = commit['files']
    if not diff_files:
        return None
    genai.configure(api_key=api_key)
    model = genai.GenerativeModel('gemini-2.0-flash')
    chat = model.start_chat()

    max_diff_length = 100000

    prompt = f"""
    You are an expert Kubernetes API reviewer, your task is to review a pull request written in the go programming language.

    Follow the following guidelines written in markdown language:
    {guidelines}

    Review the following pull request. 

    Your task is to identify potential issues and suggest concrete improvements. 

    Prioritize comments that highlight potential bugs, suggest improvements. 
    In your feedback, focus on the types.go files and validation files and functions. 
    Make sure the API changes follow the API conventions. Any changes to existing APIs should be backward compatible.

    Avoid general comments that simply acknowledge correct code or good practices.

    Provide your review comments in the following format:

    ```
    line <line_number>: <comment>
    line <line_number>: <comment>
    ...and so on
    ```

* **Adhere to Conventions:**
    * Duration fields use `fooSeconds`.
    * Condition types are `PascalCase`.
    * Constants are `CamelCase`.
    * No unsigned integers.
    * Floating-point values are avoided in `spec`.
    * Use `int32` unless `int64` is necessary.
    * `Reason` is a one-word, `CamelCase` category of cause.
    * `Message` is a human-readable phrase with specifics.
    * Label keys are lowercase with dashes.
    * Annotations are for tooling and extensions.
* **Compatibility:**
    * Added fields must have non-nil default values in all API versions.
    * New enum values must be handled safely by older clients.
    * Validation rules on spec fields cannot be relaxed nor strengthened.
    * Changes must be round-trippable with no loss of information.
* **Changes:**
    * New fields should be optional and added in a new API version if possible.
    * Singular fields should not be made plural without careful consideration of compatibility.
    * Avoid renaming fields within the same API version.
    * When adding new fields or enum values, use feature gates to control enablement and ensure compatibility with older API servers.

    """
    if max_examples < len(pr_comments):
        pr_comments =  random.sample(pr_comments, max_examples)
    for example in pr_comments:
        example_count += 1
        prompt += commit_prompt_template(example, 0, is_example=True)
    is_first_prompt = True
    prompt += commit_prompt_template(pr, -1, is_example=False)
    file_comments = []
    for diff_file in diff_files:
        diff = diff_file['patch']
        if len(diff) > max_diff_length:
            diff = diff[:max_diff_length] + "\n... (truncated due to length limit)..."
        diff_prompt = f"""
path: 
{diff_file['filename']}
diff: 
{diff}
Provide your review comments in the following format:
```
line <line_number>: <comment>
line <line_number>: <comment>
...and so on
```
"""
        if is_first_prompt:
            diff_prompt = prompt + diff_prompt
            is_first_prompt = False
        print("me:", diff_prompt)
        print('****************************')
        response = chat.send_message(diff_prompt)
        if response and response.text:
            print("gemini:", response.text)
            file_comments.append({"gemini_response":response.text, "file": diff_file})
        else:
            print("=== Gemini Response (Empty) ===")
    return file_comments

def post_github_review_comments(repo_name, pr_number, diff_file, review_comment, github_token):
    """Posts review comments to GitHub PR, annotating specific lines."""
    global total_comments_posted  # Declare total_comments_posted as global
    g = Github(github_token)
    repo = g.get_repo(repo_name)
    pr = repo.get_pull(pr_number)

    if review_comment:
        commits = list(pr.get_commits())
        if not commits:
            print(f"WARNING: No commits for PR {pr_number}. Posting general comment for {diff_file['filename']}.")
            pr.create_issue_comment(f"Review for {diff_file['filename']}:\n{review_comment}")
            return

        latest_commit = commits[-1]
        diff_lines = diff_file['patch'].splitlines()

        # Use regex to find line numbers and comments
        line_comments = [(int(match.group(1)), match.group(2).strip())
                         for match in re.finditer(r"line (\d+): (.*)", review_comment)]

        for line_num, comment in line_comments:
            if total_comments_posted >= MAX_COMMENTS:
                print("Comment limit reached.")
                break
            try:
                corrected_line_num = None
                right_side_line = 0
                current_line = 0

                for diff_line in diff_lines:
                    if diff_line.startswith("@@"):
                        # Extract right-side line number from hunk info
                        hunk_info = diff_line.split("@@")[1].strip()
                        right_side_info = hunk_info.split("+")[1].split(" ")[0]
                        right_side_line = int(right_side_info.split(",")[0])
                        current_line = right_side_line - 1

                    elif diff_line.startswith("+"):
                        current_line += 1
                        if current_line == line_num:
                            corrected_line_num = current_line
                            break

                    elif not diff_line.startswith("-") and not diff_line.startswith("@@"): #count unchanged lines.
                        current_line += 1
                        if current_line == line_num:
                            corrected_line_num = current_line
                            break

                if corrected_line_num:
                    pr.create_review_comment(
                        body=comment,
                        commit=latest_commit,
                        path=diff_file['filename'],
                        line=corrected_line_num,
                        side="RIGHT",
                    )
                    total_comments_posted += 1
                    print(f"Review comments for {diff_file['filename']} posted.")
                else:
                    print(f"WARNING: Could not find line {line_num} in {diff_file['filename']}.")
                    print(f"Diff file: {diff_file['filename']}")
                    print(f"Gemini comment: {comment}")

            except Exception as e:
                print(f"ERROR: Failed to create comment for line {line_num} in {diff_file['filename']}: {e}")

    else:
        print(f"Gemini returned no response for {diff_file['filename']}.")

def main():
    """Main function to orchestrate Gemini PR review."""
    api_key = os.environ.get('GEMINI_API_KEY')
    pr_number = 10 # int(os.environ.get('PR_NUMBER'))
    repo_name = "richabanker/kubernetes" # os.environ.get('GITHUB_REPOSITORY')
    github_token = os.environ.get('GITHUB_TOKEN')

    pr = get_pr_details(repo_name, pr_number, github_token)
    label = "api-review"
    pr_comments_blob = 'json/pr_comments_liggit_thockin.json'
    pr_comments = download_file_as_json("hackathon-2025-sme-code-review-train", pr_comments_blob)
    if not pr_comments:
        pr_comments = crawl_pr_comments_to_gcs("kubernetes/kubernetes", label, github_token, 
                                               "hackathon-2025-sme-code-review-train", pr_comments_blob,pr_limit=100)
    print(f"loaded {len(pr_comments)} example pr comments")
    guidelines = download_and_combine_guidelines("hackathon-2025-sme-code-review-train", "guidelines/")
    if not guidelines:
        print("Warning: No guidelines loaded.")

    file_review_comments = generate_gemini_review_with_annotations_multi_turn(pr, api_key, guidelines, pr_comments)
    for diff_file in file_review_comments:
        post_github_review_comments(repo_name, pr_number, diff_file['file'], diff_file['gemini_response'], github_token)

if __name__ == "__main__":
    main()
